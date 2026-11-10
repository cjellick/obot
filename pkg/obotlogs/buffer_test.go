package obotlogs

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSearch(t *testing.T) {
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	b := NewBuffer(4)
	b.Add("stderr", `{"time":"2026-11-10T12:00:00Z","severity":"INFO","message":"server started"}`, base)
	b.Add("stderr", `{"time":"2026-11-10T12:01:00Z","severity":"WARNING","message":"slow request","path":"/api/vmcps"}`, base)
	b.Add("stdout", `time=2026-11-10T12:02:00Z level=ERROR msg="failed to deploy" server=ms1abc`, base.Add(2*time.Minute))
	b.Add("stdout", "plain line without a level", base.Add(3*time.Minute))
	b.Add("stderr", `{"time":"2026-11-10T12:04:00Z","severity":"ERROR","message":"bifrost: provider timeout"}`, base)

	tests := []struct {
		name  string
		query Query
		want  []string
	}{
		{
			name:  "all, oldest evicted",
			query: Query{},
			want:  []string{"slow request", "failed to deploy", "plain line", "provider timeout"},
		},
		{
			name:  "warn and above",
			query: Query{MinLevel: "warn"},
			want:  []string{"slow request", "failed to deploy", "provider timeout"},
		},
		{
			name:  "errors only",
			query: Query{MinLevel: "error"},
			want:  []string{"failed to deploy", "provider timeout"},
		},
		{
			name:  "text, every word",
			query: Query{Text: "DEPLOY ms1abc"},
			want:  []string{"failed to deploy"},
		},
		{
			name:  "since",
			query: Query{Since: base.Add(150 * time.Second)},
			want:  []string{"plain line", "provider timeout"},
		},
		{
			name:  "limit keeps the newest",
			query: Query{Limit: 1},
			want:  []string{"provider timeout"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.Search(tt.query)
			if len(got.Lines) != len(tt.want) {
				t.Fatalf("got %d lines, want %d: %+v", len(got.Lines), len(tt.want), got.Lines)
			}
			for i, w := range tt.want {
				if !strings.Contains(got.Lines[i].Text, w) {
					t.Errorf("line %d = %q, want it to contain %q", i, got.Lines[i].Text, w)
				}
			}
		})
	}
}

func TestCopyLines(t *testing.T) {
	b := NewBuffer(10)
	var out bytes.Buffer
	copyLines(strings.NewReader("one\ntwo\npartial"), &out, "stdout", b)
	if out.String() != "one\ntwo\npartial" {
		t.Fatalf("forwarded %q", out.String())
	}
	got := b.Search(Query{})
	if len(got.Lines) != 3 || got.Lines[2].Text != "partial" || got.Lines[0].Stream != "stdout" {
		t.Fatalf("buffered %+v", got.Lines)
	}
}
