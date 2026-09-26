package guardrails

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPresidioFindsAndLocatesEntities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/analyze" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("unexpected authorization %q", got)
		}
		var req presidioRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Text != "héllo bob@example.com" || req.Language != "de" || req.ScoreThreshold != 0.6 || len(req.Entities) != 1 || req.Entities[0] != "EMAIL_ADDRESS" {
			t.Errorf("unexpected request %+v", req)
		}
		// Offsets are code points: "é" is one character but two bytes.
		_, _ = w.Write([]byte(`[{"entity_type":"EMAIL_ADDRESS","start":6,"end":21,"score":0.95},{"entity_type":"PERSON","start":0,"end":5,"score":0.3}]`))
	}))
	defer srv.Close()

	c, err := newChecker("presidio", map[string]any{
		"analyzerURL":    srv.URL + "/",
		"language":       "de",
		"entities":       []any{"EMAIL_ADDRESS"},
		"scoreThreshold": 0.6,
	}, map[string]string{"apiKey": "secret"}, Deps{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	segments := []Segment{{Role: RoleUser}, {Role: RoleUser, Text: "héllo bob@example.com"}}
	result, err := c.Check(t.Context(), CheckInput{Stage: StageInput, Segments: segments})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("expected one finding, got %+v", result.Findings)
	}
	f := result.Findings[0]
	if f.Category != "pii" || f.Message != "EMAIL_ADDRESS detected" || f.Count != 1 || f.Score != 0.95 {
		t.Fatalf("unexpected finding %+v", f)
	}
	if s := f.Spans[0]; s.Segment != 1 || s.Start != 7 || s.End != 22 {
		t.Fatalf("unexpected span %+v", s)
	}
	if got := ApplySpans(segments, f.Spans)[1].Text; got != "héllo [REDACTED:EMAIL_ADDRESS]" {
		t.Fatalf("unexpected redaction %q", got)
	}
}

func TestPresidioNoFindingsAndErrors(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("no API key is configured, but got an authorization header")
		}
		if fail {
			http.Error(w, `{"error":"No language provided"}`, http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c, err := newChecker("presidio", map[string]any{"analyzerURL": srv.URL}, nil, Deps{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	in := CheckInput{Stage: StageOutput, Segments: []Segment{{Role: RoleAssistant, Text: "nothing to see"}}}
	if result, err := c.Check(t.Context(), in); err != nil || len(result.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v, %v", result, err)
	}
	fail = true
	if _, err := c.Check(t.Context(), in); err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "No language provided") {
		t.Fatalf("expected an error with the status and body, got %v", err)
	}
	if _, err := newChecker("presidio", nil, nil, Deps{}); err == nil {
		t.Fatal("the analyzer URL should be required")
	}
}

func TestChunkByCodePoints(t *testing.T) {
	text := "é123456789abcdefgh ijklmnopqrstuvwxyz"
	chunks := chunkByCodePoints(text, 20)
	var rebuilt strings.Builder
	for _, c := range chunks {
		if n := len([]rune(c.Text)); n > 20 {
			t.Fatalf("chunk %q has %d code points", c.Text, n)
		}
		if text[c.Offset:c.Offset+len(c.Text)] != c.Text {
			t.Fatalf("chunk %q has the wrong offset %d", c.Text, c.Offset)
		}
		rebuilt.WriteString(c.Text)
	}
	if rebuilt.String() != text {
		t.Fatalf("chunks don't cover the text: %+v", chunks)
	}
	if chunks[0].Text != "é123456789abcdefgh " {
		t.Fatalf("expected a break after whitespace, got %+v", chunks)
	}
}
