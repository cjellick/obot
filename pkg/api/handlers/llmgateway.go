package handlers

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/bifrost"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
)

// LLMGatewayHandler serves Obot's management API for the embedded Bifrost gateway. Most
// of it is a guarded pass-through to Bifrost's own management API; Obot adds budget
// bindings (which tie Obot users and groups to Bifrost virtual keys), model sync, and
// import of legacy model provider credentials.
type LLMGatewayHandler struct {
	engine *bifrost.Engine
}

func NewLLMGatewayHandler(engine *bifrost.Engine) *LLMGatewayHandler {
	return &LLMGatewayHandler{engine: engine}
}

// allowedBifrostAPIPrefixes are the Bifrost management routes exposed through Obot. Bifrost
// also has MCP, skills, prompt repository, and session APIs; Obot has its own versions of
// those features, so they stay hidden.
var allowedBifrostAPIPrefixes = []string{
	"providers",
	"keys",
	"models",
	"governance/",
	"logs",
	"config",
	"plugins",
	"cache/",
	"routing/",
	"version",
	"pricing/",
	"proxy-config",
	"feature-flags",
}

// LLMGatewayStatus describes the embedded gateway.
type LLMGatewayStatus struct {
	Enabled             bool   `json:"enabled"`
	Ready               bool   `json:"ready"`
	Error               string `json:"error,omitempty"`
	HasProviders        bool   `json:"hasProviders"`
	ModelProviderName   string `json:"modelProviderName"`
	ProxyPath           string `json:"proxyPath"`
	ModelsBackPopulated bool   `json:"modelsBackPopulated"`
}

func (h *LLMGatewayHandler) Status(req api.Context) error {
	status := LLMGatewayStatus{
		Enabled:           h.engine != nil,
		ModelProviderName: system.BifrostModelProvider,
		ProxyPath:         "/api/llm-proxy/bifrost",
	}
	if h.engine != nil {
		status.Ready = h.engine.Ready()
		if err := h.engine.Err(); err != nil {
			status.Error = err.Error()
		}
		status.HasProviders = h.engine.HasUpstreamProviders(req.Context())
	}
	var mp v1.ModelProvider
	if err := req.Get(&mp, system.BifrostModelProvider); err == nil && mp.Status.ModelsBackPopulated != nil {
		status.ModelsBackPopulated = *mp.Status.ModelsBackPopulated
	}
	return req.Write(status)
}

func (h *LLMGatewayHandler) requireEngine() error {
	if h.engine == nil {
		return types.NewErrHTTP(http.StatusServiceUnavailable, "the LLM gateway is disabled")
	}
	if !h.engine.Ready() {
		if err := h.engine.Err(); err != nil {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "the LLM gateway failed to start: "+err.Error())
		}
		return types.NewErrHTTP(http.StatusServiceUnavailable, "the LLM gateway is starting")
	}
	return nil
}

// Proxy forwards /api/llm-gateway/bifrost/{path...} to Bifrost's /api/{path...}.
func (h *LLMGatewayHandler) Proxy(req api.Context) error {
	if err := h.requireEngine(); err != nil {
		return err
	}

	path := strings.TrimPrefix(req.PathValue("path"), "/")
	allowed := false
	for _, prefix := range allowedBifrostAPIPrefixes {
		if path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
			allowed = true
			break
		}
	}
	if !allowed {
		return types.NewErrNotFound("LLM gateway API %q is not available", path)
	}

	body, err := req.Body(api.BodyOptions{MaxBytes: 32 * 1024 * 1024})
	if err != nil {
		return err
	}

	out, err := http.NewRequestWithContext(req.Context(), req.Method, (&url.URL{Path: "/api/" + path, RawQuery: req.URL.RawQuery}).String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	if ct := req.Request.Header.Get("Content-Type"); ct != "" {
		out.Header.Set("Content-Type", ct)
	}
	out.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.engine.ServeHTTP(rec, out)

	if req.Method != http.MethodGet && rec.Code < 300 {
		h.afterMutation(req.Context(), req, path)
	}

	for k, vs := range rec.Header() {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			req.ResponseWriter.Header().Add(k, v)
		}
	}
	req.ResponseWriter.WriteHeader(rec.Code)
	_, err = req.ResponseWriter.Write(rec.Body.Bytes())
	return err
}

// afterMutation keeps Obot in step with changes made through the proxy.
func (h *LLMGatewayHandler) afterMutation(ctx context.Context, req api.Context, path string) {
	switch {
	case strings.HasPrefix(path, "providers"), strings.HasPrefix(path, "models"), strings.HasPrefix(path, "keys"):
		if err := bifrost.TriggerModelSync(ctx, req.Storage); err != nil {
			slog.Warn("failed to trigger LLM gateway model sync", "error", err)
		}
	case strings.HasPrefix(path, "governance/virtual-keys"):
		bifrost.ForgetVirtualKey("")
		if req.Method == http.MethodDelete {
			if vkID, _, _ := strings.Cut(strings.TrimPrefix(path, "governance/virtual-keys/"), "/"); vkID != "" {
				if err := req.GatewayClient.DeleteLLMGatewayBudgetBindings(ctx, vkID); err != nil {
					slog.Warn("failed to delete LLM gateway budget bindings", "virtualKeyID", vkID, "error", err)
				}
			}
		}
	}
}

func (h *LLMGatewayHandler) SyncModels(req api.Context) error {
	if err := h.requireEngine(); err != nil {
		return err
	}
	if err := bifrost.TriggerModelSync(req.Context(), req.Storage); err != nil {
		return err
	}
	req.WriteHeader(http.StatusNoContent)
	return nil
}

// LLMGatewayBudgetBinding is the API shape of a budget binding subject.
type LLMGatewayBudgetBinding struct {
	VirtualKeyID string `json:"virtualKeyID"`
	SubjectType  string `json:"subjectType"`
	SubjectID    string `json:"subjectID,omitempty"`
}

func (h *LLMGatewayHandler) ListBudgetBindings(req api.Context) error {
	bindings, err := req.GatewayClient.ListLLMGatewayBudgetBindings(req.Context(), req.URL.Query().Get("virtualKeyID"))
	if err != nil {
		return err
	}
	items := make([]LLMGatewayBudgetBinding, 0, len(bindings))
	for _, b := range bindings {
		items = append(items, LLMGatewayBudgetBinding{VirtualKeyID: b.VirtualKeyID, SubjectType: b.SubjectType, SubjectID: b.SubjectID})
	}
	return req.Write(map[string]any{"items": items})
}

func (h *LLMGatewayHandler) SetBudgetBindings(req api.Context) error {
	if err := h.requireEngine(); err != nil {
		return err
	}
	vkID := req.PathValue("virtual_key_id")

	var in struct {
		Subjects []LLMGatewayBudgetBinding `json:"subjects"`
	}
	if err := req.Read(&in); err != nil {
		return types.NewErrBadRequest("invalid budget binding request: %v", err)
	}

	// The virtual key must exist, or bound callers would be locked out.
	if err := h.engine.Do(req.Context(), http.MethodGet, "/api/governance/virtual-keys/"+url.PathEscape(vkID), nil, nil); err != nil {
		var apiErr *bifrost.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return types.NewErrNotFound("virtual key %q not found", vkID)
		}
		return err
	}

	bindings := make([]gatewaytypes.LLMGatewayBudgetBinding, 0, len(in.Subjects))
	for _, s := range in.Subjects {
		switch s.SubjectType {
		case gatewaytypes.LLMGatewayBindingSubjectUser, gatewaytypes.LLMGatewayBindingSubjectGroup:
			if s.SubjectID == "" {
				return types.NewErrBadRequest("subjectID is required for %s subjects", s.SubjectType)
			}
		case gatewaytypes.LLMGatewayBindingSubjectEveryone:
		default:
			return types.NewErrBadRequest("invalid subject type %q", s.SubjectType)
		}
		bindings = append(bindings, gatewaytypes.LLMGatewayBudgetBinding{SubjectType: s.SubjectType, SubjectID: s.SubjectID})
	}

	if err := req.GatewayClient.ReplaceLLMGatewayBudgetBindings(req.Context(), vkID, bindings); err != nil {
		return err
	}
	return h.ListBudgetBindingsFor(req, vkID)
}

func (h *LLMGatewayHandler) ListBudgetBindingsFor(req api.Context, vkID string) error {
	q := req.URL.Query()
	q.Set("virtualKeyID", vkID)
	req.URL.RawQuery = q.Encode()
	return h.ListBudgetBindings(req)
}

// ImportLegacyProviders copies the credentials of configured legacy model providers into
// the gateway, so an existing installation can switch over without re-entering keys.
func (h *LLMGatewayHandler) ImportLegacyProviders(req api.Context) error {
	if err := h.requireEngine(); err != nil {
		return err
	}
	results, err := h.engine.ImportLegacyModelProviders(req.Context(), req.Storage, req.GatewayClient)
	if err != nil {
		return err
	}
	if err := bifrost.TriggerModelSync(req.Context(), req.Storage); err != nil {
		slog.Warn("failed to trigger LLM gateway model sync", "error", err)
	}
	return req.Write(map[string]any{"items": results})
}
