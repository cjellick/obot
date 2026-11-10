package builtinmcp

import (
	"strings"
	"testing"
)

func TestFitLines(t *testing.T) {
	tests := []struct {
		name        string
		lines       []string
		lineLimit   int
		budget      int
		wantLines   []string
		wantDropped int
	}{
		{
			name:      "everything fits",
			lines:     []string{"a", "b"},
			lineLimit: 10,
			budget:    100,
			wantLines: []string{"a", "b"},
		},
		{
			name:        "keeps the newest",
			lines:       []string{"old", "mid", "new"},
			lineLimit:   10,
			budget:      15,
			wantLines:   []string{"mid", "new"},
			wantDropped: 1,
		},
		{
			name:      "cuts long lines",
			lines:     []string{strings.Repeat("x", 20)},
			lineLimit: 5,
			budget:    100,
			wantLines: []string{"xxxxx…"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, dropped := fitLines(tt.lines, tt.lineLimit, tt.budget)
			if strings.Join(got, "|") != strings.Join(tt.wantLines, "|") || dropped != tt.wantDropped {
				t.Errorf("got %q dropped %d, want %q dropped %d", got, dropped, tt.wantLines, tt.wantDropped)
			}
		})
	}
}
