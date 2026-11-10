package handlers

import (
	"errors"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/provenance"
)

// customLabelTestRequest is sample content and an optional origin to run a
// custom label's deterministic detectors against.
type customLabelTestRequest struct {
	Label provenance.CustomLabel `json:"label"`
	Text  string                 `json:"text"`
	// Scope (catalog-entry:<id> or vmcp:<id>), Tool and Host describe where
	// the sample came from, for source detectors.
	Scope string `json:"scope,omitempty"`
	Tool  string `json:"tool,omitempty"`
	Host  string `json:"host,omitempty"`
}

type customLabelTestHit struct {
	Detector string   `json:"detector"`
	Detail   string   `json:"detail"`
	Matches  []string `json:"matches,omitempty"`
}

type customLabelTestResponse struct {
	Matched bool                 `json:"matched"`
	Hits    []customLabelTestHit `json:"hits"`
}

func (h *ProvenanceHandler) ListCustomLabels(req api.Context) error {
	labels, err := h.engine.Store().ListCustomLabels(req.Context())
	if err != nil {
		return err
	}
	if labels == nil {
		labels = []provenance.CustomLabel{}
	}
	return req.Write(map[string]any{"items": labels})
}

func (h *ProvenanceHandler) GetCustomLabel(req api.Context) error {
	label, err := h.engine.Store().GetCustomLabel(req.Context(), provenance.Label(req.PathValue("name")))
	if errors.Is(err, provenance.ErrNotFound) {
		return types.NewErrNotFound("custom label %q not found", req.PathValue("name"))
	} else if err != nil {
		return err
	}
	return req.Write(label)
}

func (h *ProvenanceHandler) CreateCustomLabel(req api.Context) error {
	var in provenance.CustomLabel
	if err := req.Read(&in); err != nil {
		return types.NewErrBadRequest("invalid request: %v", err)
	}
	if err := in.Validate(); err != nil {
		return types.NewErrBadRequest("%v", err)
	}
	if _, err := h.engine.Store().GetCustomLabel(req.Context(), in.Name); err == nil {
		return types.NewErrAlreadyExists("custom label %q already exists", in.Name)
	} else if !errors.Is(err, provenance.ErrNotFound) {
		return err
	}
	now := time.Now().UTC()
	in.CreatedBy, in.CreatedAt, in.UpdatedAt = callerIdentity(req), now, now
	if err := h.engine.Store().PutCustomLabel(req.Context(), &in); err != nil {
		return err
	}
	h.engine.Invalidate()
	return req.WriteCreated(in)
}

func (h *ProvenanceHandler) UpdateCustomLabel(req api.Context) error {
	name := provenance.Label(req.PathValue("name"))
	existing, err := h.engine.Store().GetCustomLabel(req.Context(), name)
	if errors.Is(err, provenance.ErrNotFound) {
		return types.NewErrNotFound("custom label %q not found", name)
	} else if err != nil {
		return err
	}
	var in provenance.CustomLabel
	if err := req.Read(&in); err != nil {
		return types.NewErrBadRequest("invalid request: %v", err)
	}
	// The name is the label policies test; renaming would silently break them.
	in.Name = name
	if err := in.Validate(); err != nil {
		return types.NewErrBadRequest("%v", err)
	}
	in.CreatedBy, in.CreatedAt, in.UpdatedAt = existing.CreatedBy, existing.CreatedAt, time.Now().UTC()
	if err := h.engine.Store().PutCustomLabel(req.Context(), &in); err != nil {
		return err
	}
	h.engine.Invalidate()
	return req.Write(in)
}

func (h *ProvenanceHandler) DeleteCustomLabel(req api.Context) error {
	if err := h.engine.Store().DeleteCustomLabel(req.Context(), provenance.Label(req.PathValue("name"))); err != nil {
		return err
	}
	h.engine.Invalidate()
	return writeNoContent(req)
}

// TestCustomLabel runs a draft label's pattern and source detectors against
// sample content without saving anything. The AI detector is not run.
func (h *ProvenanceHandler) TestCustomLabel(req api.Context) error {
	var in customLabelTestRequest
	if err := req.Read(&in); err != nil {
		return types.NewErrBadRequest("invalid request: %v", err)
	}
	label := in.Label
	if label.Name == "" {
		label.Name = "draft-label"
	}
	if label.Description == "" {
		label.Description = "draft"
	}
	label.AI = true // a test needs no detector of its own
	if err := label.Validate(); err != nil {
		return types.NewErrBadRequest("%v", err)
	}
	set := provenance.CompileCustomLabels([]provenance.CustomLabel{label})
	hits := set.MatchPatterns(in.Text)
	hits = append(hits, set.MatchSources(provenance.ContentOrigin{Scope: in.Scope, Tool: in.Tool, Host: in.Host})...)
	out := customLabelTestResponse{Matched: len(hits) > 0, Hits: []customLabelTestHit{}}
	for _, hit := range hits {
		out.Hits = append(out.Hits, customLabelTestHit{Detector: hit.Detector, Detail: hit.Detail, Matches: hit.Matches})
	}
	return req.Write(out)
}
