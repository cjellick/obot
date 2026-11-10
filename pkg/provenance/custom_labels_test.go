package provenance

import (
	"context"
	"strings"
	"testing"
	"time"
)

// recordingContentClassifier records the vocabulary it was given and returns
// canned labels.
type recordingContentClassifier struct {
	labels Labels
	got    []LabelDefinition
}

func (f *recordingContentClassifier) ClassifyContent(_ context.Context, in ContentToClassify) (ContentClassification, error) {
	f.got = in.Labels
	return ContentClassification{Labels: f.labels, Rationale: "mentions the Atlas launch plan", Model: "fake"}, nil
}

func atlasLabel() CustomLabel {
	return CustomLabel{
		Name:        "project-atlas",
		Description: "Anything about Project Atlas, the unannounced product launch",
		Patterns: []LabelPattern{
			{Type: PatternKeyword, Value: "Project Atlas"},
			{Type: PatternRegex, Value: `\bATL-\d{4}\b`},
		},
	}
}

func putCustomLabel(t *testing.T, e *Engine, l CustomLabel) {
	t.Helper()
	if err := l.Validate(); err != nil {
		t.Fatalf("validate %s: %v", l.Name, err)
	}
	if err := e.store.PutCustomLabel(t.Context(), &l); err != nil {
		t.Fatalf("put custom label: %v", err)
	}
	e.Invalidate()
}

func TestCustomLabelValidate(t *testing.T) {
	cases := []struct {
		name    string
		label   CustomLabel
		wantErr string
	}{
		{
			name:  "valid patterns",
			label: atlasLabel(),
		},
		{
			name:  "valid AI only",
			label: CustomLabel{Name: "board-minutes", Description: "Minutes of board meetings", AI: true},
		},
		{
			name:    "built-in name",
			label:   CustomLabel{Name: "pii", Description: "x", AI: true},
			wantErr: "built-in",
		},
		{
			name:    "capability name",
			label:   CustomLabel{Name: "exec", Description: "x", AI: true},
			wantErr: "built-in",
		},
		{
			name:    "bad name",
			label:   CustomLabel{Name: "Customer Data!", Description: "x", AI: true},
			wantErr: "lowercase",
		},
		{
			name:    "double hyphen",
			label:   CustomLabel{Name: "a--b", Description: "x", AI: true},
			wantErr: "lowercase",
		},
		{
			name:    "no description",
			label:   CustomLabel{Name: "codename", AI: true},
			wantErr: "description",
		},
		{
			name:    "no detector",
			label:   CustomLabel{Name: "codename", Description: "x"},
			wantErr: "at least one detector",
		},
		{
			name: "invalid regex",
			label: CustomLabel{
				Name:        "codename",
				Description: "x",
				Patterns:    []LabelPattern{{Type: PatternRegex, Value: `ATL-(\d+`}},
			},
			wantErr: "invalid regular expression",
		},
		{
			name: "regex matching empty",
			label: CustomLabel{
				Name:        "codename",
				Description: "x",
				Patterns:    []LabelPattern{{Type: PatternRegex, Value: `\d*`}},
			},
			wantErr: "matches empty text",
		},
		{
			name: "unknown source",
			label: CustomLabel{
				Name:        "codename",
				Description: "x",
				Sources:     []LabelSource{{Type: "bucket", ID: "x"}},
			},
			wantErr: "unknown source type",
		},
		{
			name: "host with path",
			label: CustomLabel{
				Name:        "codename",
				Description: "x",
				Sources:     []LabelSource{{Type: SourceHost, ID: "example.com/api"}},
			},
			wantErr: "host name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.label.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCustomLabelMatchPatterns(t *testing.T) {
	set := CompileCustomLabels([]CustomLabel{atlasLabel(), {
		Name:        "codenames",
		Description: "Internal codenames",
		Patterns:    []LabelPattern{{Type: PatternKeyword, Value: "bluebird"}, {Type: PatternKeyword, Value: "nightjar"}},
	}})
	cases := []struct {
		name       string
		text       string
		wantLabels []Label
		wantDetail string
		wantMatch  string
	}{
		{
			name:       "keyword ignores case and spacing",
			text:       "Notes on project\n  ATLAS timeline",
			wantLabels: []Label{"project-atlas"},
			wantDetail: `matched keyword "Project Atlas"`,
		},
		{
			name:       "regex",
			text:       "Ticket ATL-1234 is blocked",
			wantLabels: []Label{"project-atlas"},
			wantDetail: `matched pattern /\bATL-\d{4}\b/`,
			wantMatch:  "ATL-1234",
		},
		{
			name: "keyword needs a whole word",
			text: "The bluebirds and nightjars sang",
		},
		{
			name:       "term list",
			text:       "Ship nightjar before bluebird",
			wantLabels: []Label{"codenames"},
			wantDetail: `matched keyword "bluebird"`,
		},
		{
			name: "no match",
			text: "Nothing to see here",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits := set.MatchPatterns(tc.text)
			var got []Label
			for _, h := range hits {
				got = append(got, h.Label)
				if h.Detector != DetectorPattern {
					t.Errorf("detector = %q, want pattern", h.Detector)
				}
			}
			if strings.Join(NewLabels(got...).Strings(), ",") != strings.Join(NewLabels(tc.wantLabels...).Strings(), ",") {
				t.Fatalf("labels = %v, want %v", got, tc.wantLabels)
			}
			if tc.wantDetail != "" && hits[0].Detail != tc.wantDetail {
				t.Errorf("detail = %q, want %q", hits[0].Detail, tc.wantDetail)
			}
			if tc.wantMatch != "" && (len(hits[0].Matches) == 0 || hits[0].Matches[0] != tc.wantMatch) {
				t.Errorf("matches = %v, want %q", hits[0].Matches, tc.wantMatch)
			}
		})
	}
}

func TestCustomLabelMatchSources(t *testing.T) {
	set := CompileCustomLabels([]CustomLabel{
		{
			Name:        "crm-data",
			Description: "Results of the CRM",
			Sources: []LabelSource{
				{Type: SourceCatalogEntry, ID: "salesforce", Name: "Salesforce"},
				{Type: SourceVMCP, ID: "vmcp-1", Tool: "crm_lookup"},
			},
		},
		{
			Name:        "partner-feed",
			Description: "Partner API responses",
			Sources:     []LabelSource{{Type: SourceHost, ID: "*.partner.example"}},
		},
	})
	cases := []struct {
		name       string
		origin     ContentOrigin
		want       Label
		wantDetail string
	}{
		{
			name:       "catalog entry, any tool",
			origin:     ContentOrigin{Scope: "catalog-entry:salesforce", Tool: "query"},
			want:       "crm-data",
			wantDetail: "from server Salesforce",
		},
		{
			name:       "vMCP tool",
			origin:     ContentOrigin{Scope: "vmcp:vmcp-1", Tool: "crm_lookup"},
			want:       "crm-data",
			wantDetail: "from vMCP vmcp-1 tool crm_lookup",
		},
		{
			name:   "vMCP other tool",
			origin: ContentOrigin{Scope: "vmcp:vmcp-1", Tool: "search"},
		},
		{
			name:   "other server",
			origin: ContentOrigin{Scope: "catalog-entry:github", Tool: "query"},
		},
		{
			name:       "host wildcard",
			origin:     ContentOrigin{Host: "api.partner.example:443"},
			want:       "partner-feed",
			wantDetail: "from host *.partner.example",
		},
		{
			name:   "other host",
			origin: ContentOrigin{Host: "partner.example.com"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits := set.MatchSources(tc.origin)
			if tc.want == "" {
				if len(hits) != 0 {
					t.Fatalf("hits = %v, want none", hits)
				}
				return
			}
			if len(hits) != 1 || hits[0].Label != tc.want || hits[0].Detector != DetectorSource {
				t.Fatalf("hits = %v, want one source hit for %s", hits, tc.want)
			}
			if hits[0].Detail != tc.wantDetail {
				t.Errorf("detail = %q, want %q", hits[0].Detail, tc.wantDetail)
			}
		})
	}
}

func TestTaggerLabelSet(t *testing.T) {
	defs := TaggerLabelSet([]CustomLabel{
		{Name: "board-minutes", Description: "Minutes of board meetings", AI: true},
		atlasLabel(), // patterns only: not for the tagger
	})
	names := LabelDefinitionNames(defs)
	for _, l := range ContentLabels {
		if !names.Has(l) {
			t.Errorf("built-in %s missing", l)
		}
	}
	if !names.Has("board-minutes") {
		t.Fatalf("AI custom label missing from %v", names)
	}
	if names.Has("project-atlas") {
		t.Fatalf("a custom label without AI must not reach the tagger")
	}
	for _, d := range defs {
		if d.Description == "" {
			t.Errorf("%s has no description", d.Name)
		}
		if d.Name == "board-minutes" && (!d.Custom || d.Description != "Minutes of board meetings") {
			t.Errorf("custom definition = %+v", d)
		}
	}
}

func TestUnknownPolicyLabels(t *testing.T) {
	custom := NewLabels("project-atlas")
	got := UnknownPolicyLabels(`"project-atlas" in session.labels && "pii" in action.lineage.labels || "projct-atlas" in action.labels || "confidential" in action.capabilities || 'teleport' in action.capabilities`, custom)
	if got.String() != "projct-atlas,teleport" {
		t.Fatalf("unknown = %v", got)
	}
}

func TestIngestAppliesCustomLabels(t *testing.T) {
	ctx := t.Context()
	e, _ := newTestEngine(t)
	atlas := atlasLabel()
	atlas.Confidential = true
	putCustomLabel(t, e, atlas)
	putCustomLabel(t, e, CustomLabel{
		Name:        "crm-data",
		Description: "Results of the CRM",
		Sources:     []LabelSource{{Type: SourceCatalogEntry, ID: "salesforce", Name: "Salesforce"}},
	})

	ev, err := e.Ingest(ctx, IngestRequest{
		Subject: subject, Session: key, Surface: SurfaceMCP, Kind: KindToolResult,
		Server: "Salesforce", Tool: "query", LabelScope: "catalog-entry:salesforce",
		Content: "Account notes: the ATL-2041 rollout is on track.",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []Label{"project-atlas", "crm-data", LabelConfidential} {
		if !ev.Labels.Has(l) {
			t.Errorf("event labels %v missing %s", ev.Labels, l)
		}
	}

	s, err := e.Resolve(ctx, subject, key, SurfaceMCP)
	if err != nil {
		t.Fatal(err)
	}
	origins := s.State.Origins
	if o := origins["project-atlas"]; len(o) != 1 || o[0].Detector != DetectorPattern || !strings.Contains(o[0].Detail, "ATL-") {
		t.Errorf("project-atlas origins = %+v", o)
	}
	if o := origins["crm-data"]; len(o) != 1 || o[0].Detector != DetectorSource || o[0].Detail != "from server Salesforce" {
		t.Errorf("crm-data origins = %+v", o)
	}
	// The matched identifier is fingerprinted exactly for lineage.
	exact := false
	for _, fp := range s.State.Fingerprints {
		if fp.Exact && fp.Label == "project-atlas" && fp.Hash == hashExact("ATL-2041") {
			exact = true
		}
	}
	if !exact {
		t.Errorf("no exact fingerprint for the matched pattern")
	}

	// A policy can test the custom label on the session and in the action.
	policy := Policy{
		ID: "atlas-out", Name: "No Atlas outside", Condition: `"project-atlas" in session.labels && "project-atlas" in action.labels && action.sink.external`,
		Effect: EffectDeny, Mode: ModeEnforce, Enabled: true, Priority: 999, CreatedAt: time.Now(),
	}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := e.store.PutPolicy(ctx, &policy); err != nil {
		t.Fatal(err)
	}
	e.Invalidate()
	_, d, err := e.Decide(ctx, DecideRequest{Subject: subject, Session: key, Action: Action{
		Surface: SurfaceMCP, Kind: KindToolCall, Server: "slack", Tool: "post", Sink: Sink{External: true, Writes: true},
		Text: "posting a status update on project atlas",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow || d.PolicyID != "atlas-out" {
		t.Fatalf("decision = %+v, want a deny from atlas-out", d)
	}
}

func TestContentTaggerUsesCustomLabels(t *testing.T) {
	ctx := t.Context()
	e, _ := newTestEngine(t)
	putCustomLabel(t, e, CustomLabel{Name: "roadmap", Description: "Unreleased product roadmap details", AI: true})
	putCustomLabel(t, e, atlasLabel())
	cl := &recordingContentClassifier{labels: NewLabels("roadmap", "made-up")}
	e.SetContentClassifier(cl)
	setContentTagging(t, e, ContentTagging{Enabled: true, Mode: ContentTagSync, BudgetSeconds: 3})

	ev, err := e.Ingest(ctx, taggableIngest(taggableContent))
	if err != nil {
		t.Fatal(err)
	}
	names := LabelDefinitionNames(cl.got)
	if !names.Has("roadmap") || names.Has("project-atlas") || !names.Has(LabelPII) {
		t.Fatalf("tagger vocabulary = %v", names)
	}
	if !ev.Labels.Has("roadmap") || ev.Labels.Has("made-up") {
		t.Fatalf("event labels = %v", ev.Labels)
	}
	s, err := e.Resolve(ctx, subject, key, SurfaceMCP)
	if err != nil {
		t.Fatal(err)
	}
	if o := s.State.Origins["roadmap"]; len(o) != 1 || o[0].Detector != DetectorAI || o[0].Detail == "" {
		t.Fatalf("roadmap origins = %+v", o)
	}
}
