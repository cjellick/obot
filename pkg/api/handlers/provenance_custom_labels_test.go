package handlers

import (
	"net/http"
	"testing"

	"github.com/obot-platform/obot/pkg/provenance"
)

func TestProvenanceCustomLabels(t *testing.T) {
	h, e := newProvAPITest(t)
	const base = "/api/provenance/custom-labels"
	atlas := provenance.CustomLabel{
		Name:        "project-atlas",
		Description: "Anything about Project Atlas",
		Patterns:    []provenance.LabelPattern{{Type: provenance.PatternKeyword, Value: "Project Atlas"}},
	}

	for _, bad := range []provenance.CustomLabel{
		{Name: "pii", Description: "x", AI: true},
		{Name: "codename", Description: "x", Patterns: []provenance.LabelPattern{{Type: provenance.PatternRegex, Value: "(unclosed"}}},
		{Name: "codename", Description: "x"},
	} {
		if code := provAPIStatus(t, h.CreateCustomLabel, http.MethodPost, base, bad, nil); code != http.StatusBadRequest {
			t.Fatalf("create %+v: got %d, want 400", bad, code)
		}
	}

	var created provenance.CustomLabel
	provAPIOK(t, h.CreateCustomLabel, http.MethodPost, base, atlas, nil, &created)
	if created.CreatedBy != "admin@example.com" || created.CreatedAt.IsZero() {
		t.Fatalf("created: %+v", created)
	}
	if code := provAPIStatus(t, h.CreateCustomLabel, http.MethodPost, base, atlas, nil); code != http.StatusConflict {
		t.Fatalf("duplicate create: got %d, want 409", code)
	}

	// The update keeps the name from the path and the creation metadata.
	update := atlas
	update.Name = "renamed"
	update.AI = true
	var updated provenance.CustomLabel
	provAPIOK(t, h.UpdateCustomLabel, http.MethodPut, base+"/project-atlas", update, map[string]string{"name": "project-atlas"}, &updated)
	if updated.Name != "project-atlas" || !updated.AI || updated.CreatedBy != "admin@example.com" {
		t.Fatalf("updated: %+v", updated)
	}
	if code := provAPIStatus(t, h.UpdateCustomLabel, http.MethodPut, base+"/missing", update, map[string]string{"name": "missing"}); code != http.StatusNotFound {
		t.Fatalf("update missing: got %d, want 404", code)
	}

	var list struct {
		Items []provenance.CustomLabel `json:"items"`
	}
	provAPIOK(t, h.ListCustomLabels, http.MethodGet, base, nil, nil, &list)
	if len(list.Items) != 1 || list.Items[0].Name != "project-atlas" {
		t.Fatalf("list: %+v", list)
	}
	if !provenance.LabelDefinitionNames(e.TaggerLabels(t.Context())).Has("project-atlas") {
		t.Fatalf("the tagger should see the label once AI is on")
	}

	// A condition naming the custom label is clean; a typo is flagged.
	var validation policyValidateResponse
	provAPIOK(t, h.ValidatePolicy, http.MethodPost, "/api/provenance/policies/validate", policyValidateRequest{Condition: `"project-atlas" in session.labels`}, nil, &validation)
	if !validation.Valid || len(validation.Warnings) != 0 {
		t.Fatalf("validation: %+v", validation)
	}
	provAPIOK(t, h.ValidatePolicy, http.MethodPost, "/api/provenance/policies/validate", policyValidateRequest{Condition: `"project-atlass" in session.labels`}, nil, &validation)
	if !validation.Valid || len(validation.Warnings) != 1 {
		t.Fatalf("typo validation: %+v", validation)
	}

	var tested customLabelTestResponse
	provAPIOK(t, h.TestCustomLabel, http.MethodPost, base+"/test", customLabelTestRequest{Label: atlas, Text: "notes on project atlas"}, nil, &tested)
	if !tested.Matched || len(tested.Hits) != 1 || tested.Hits[0].Detector != provenance.DetectorPattern {
		t.Fatalf("test: %+v", tested)
	}

	provAPIOK(t, h.DeleteCustomLabel, http.MethodDelete, base+"/project-atlas", nil, map[string]string{"name": "project-atlas"}, nil)
	if code := provAPIStatus(t, h.GetCustomLabel, http.MethodGet, base+"/project-atlas", nil, map[string]string{"name": "project-atlas"}); code != http.StatusNotFound {
		t.Fatalf("get deleted: got %d, want 404", code)
	}
}
