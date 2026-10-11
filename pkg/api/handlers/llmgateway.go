package handlers

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/bifrost"
	"github.com/obot-platform/obot/pkg/controller/handlers/providerconfigurationchange"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/system"
	"gorm.io/gorm"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// allowedBifrostAPIPrefixes are the Bifrost management routes exposed through Obot. Bifrost
	// also has MCP, skills, prompt repository, and session APIs; Obot has its own versions of
	// those features, so they stay hidden. Its plugins and response cache aren't exposed
	// either: Obot's guardrail plugins must stay enabled, and responses aren't cached.
	allowedBifrostAPIPrefixes = []string{
		"providers",
		"keys",
		"models",
		"governance/routing-rules",
		"governance/pricing-overrides",
		"logs",
		"config",
		"routing/",
		"version",
		"pricing/",
		"proxy-config",
		"feature-flags",
	}
)

// LLMGatewayHandler serves Obot's management API for the embedded Bifrost gateway. Most
// of it is a guarded pass-through to Bifrost's own management API; Obot adds its own
// budgets (enforced by Obot, not Bifrost), model sync, and import of legacy model provider
// credentials.
type LLMGatewayHandler struct {
	engine *bifrost.Engine
}

// LLMGatewayStatus describes the embedded gateway.
type LLMGatewayStatus struct {
	Enabled           bool   `json:"enabled"`
	Ready             bool   `json:"ready"`
	Error             string `json:"error,omitempty"`
	HasProviders      bool   `json:"hasProviders"`
	ModelProviderName string `json:"modelProviderName"`
	ProxyPath         string `json:"proxyPath"`
}

func NewLLMGatewayHandler(engine *bifrost.Engine) *LLMGatewayHandler {
	return &LLMGatewayHandler{engine: engine}
}

func (h *LLMGatewayHandler) Status(req api.Context) error {
	status := LLMGatewayStatus{
		Enabled:           true,
		ModelProviderName: system.LLMGatewayModelProvider,
		ProxyPath:         "/api/llm-proxy",
		Ready:             h.engine.Ready(),
		HasProviders:      h.engine.HasUpstreamProviders(req.Context()),
	}
	if err := h.engine.Err(); err != nil {
		status.Error = bifrost.ScrubEngineName(err.Error())
	}
	return req.Write(status)
}

// withProviderDefaults fills in the network settings a new provider leaves unset, such as
// retries, which also let requests fail over between its keys.
func withProviderDefaults(body []byte) ([]byte, error) {
	var provider map[string]any
	if err := json.Unmarshal(body, &provider); err != nil {
		return nil, types.NewErrBadRequest("invalid provider: %v", err)
	}
	bifrost.ApplyProviderDefaults(provider)
	return json.Marshal(provider)
}

func (h *LLMGatewayHandler) requireEngine() error {
	if !h.engine.Ready() {
		if err := h.engine.Err(); err != nil {
			return types.NewErrHTTP(http.StatusServiceUnavailable, "the LLM gateway failed to start: "+err.Error())
		}
		return types.NewErrHTTP(http.StatusServiceUnavailable, "the LLM gateway is starting")
	}
	return nil
}

// Proxy forwards /api/llm-gateway/admin/{path...} to Bifrost's /api/{path...}.
func (h *LLMGatewayHandler) Proxy(req api.Context) error {
	if err := h.requireEngine(); err != nil {
		return err
	}

	path, ok := allowedBifrostAPIPath(req.PathValue("path"), req.URL.EscapedPath())
	if !ok {
		return types.NewErrNotFound("LLM gateway API %q is not available", req.PathValue("path"))
	}

	body, err := req.Body(api.BodyOptions{MaxBytes: 32 * 1024 * 1024})
	if err != nil {
		return err
	}
	if len(body) > 0 {
		if err := rejectSecretReferences(body); err != nil {
			return err
		}
	}
	if req.Method == http.MethodPost && path == "providers" {
		if body, err = withProviderDefaults(body); err != nil {
			return err
		}
	}

	ctx := req.Context()
	if req.Method != http.MethodGet {
		// Once a change reaches the gateway it must finish and be announced to the other
		// replicas even if the client goes away, so the client can't cancel it midway.
		ctx = context.WithoutCancel(ctx)
	}
	out, err := http.NewRequestWithContext(ctx, req.Method, (&url.URL{Path: "/api/" + path, RawQuery: req.URL.RawQuery}).String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	if ct := req.Request.Header.Get("Content-Type"); ct != "" {
		out.Header.Set("Content-Type", ct)
	}
	out.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	if req.Method == http.MethodGet {
		h.engine.ServeHTTP(rec, out)
	} else if err := h.engine.Change(ctx, func() (bool, bool) {
		h.engine.ServeHTTP(rec, out)
		switch {
		case rec.Code < 300:
			h.afterMutation(ctx, req, path)
			return true, true
		case rec.Code < 500:
			// Rejected; nothing changed.
			return false, false
		default:
			// The change may have been saved before the failure, in the stores, in memory or
			// both, so every replica, this one included, reloads to be safe.
			return true, false
		}
	}, announceLLMGatewayChange(req.Storage)); err != nil {
		return types.NewErrHTTP(http.StatusInternalServerError, err.Error())
	}

	if rec.Code >= http.StatusBadRequest {
		return engineError(rec.Code, rec.Body.Bytes())
	}
	for k, vs := range rec.Header() {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			req.ResponseWriter.Header().Add(k, v)
		}
	}
	req.WriteHeader(rec.Code)
	_, err = req.ResponseWriter.Write(rec.Body.Bytes())
	return err
}

// engineError turns an error response from the engine's API into an Obot API error carrying
// only its message, so the engine's own error format and name don't reach clients.
func engineError(code int, body []byte) error {
	var errBody struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &errBody) == nil {
		message = cmp.Or(errBody.Error.Message, errBody.Message)
	}
	if message == "" {
		message = http.StatusText(code)
	}
	return types.NewErrHTTP(code, bifrost.ScrubEngineName(message))
}

// allowedBifrostAPIPath validates the requested Bifrost API path against the allowlist. The
// path is matched after cleaning, and any dot segment or encoded separator is refused, so a
// path like "providers/%2e%2e/session" can't reach an API that isn't listed.
func allowedBifrostAPIPath(raw, escaped string) (string, bool) {
	raw = strings.TrimPrefix(raw, "/")
	lowerEscaped := strings.ToLower(escaped)
	if strings.Contains(lowerEscaped, "%2f") || strings.Contains(lowerEscaped, "%2e") || strings.Contains(lowerEscaped, "%5c") {
		return "", false
	}
	for segment := range strings.SplitSeq(raw, "/") {
		if segment == "." || segment == ".." {
			return "", false
		}
	}
	if strings.Contains(raw, "\\") {
		return "", false
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+raw), "/")
	if cleaned != strings.TrimSuffix(raw, "/") {
		return "", false
	}
	for _, prefix := range allowedBifrostAPIPrefixes {
		prefix = strings.TrimSuffix(prefix, "/")
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return cleaned, true
		}
	}
	return "", false
}

// rejectSecretReferences refuses request bodies that ask Bifrost to resolve a value from the
// Obot server's environment or a vault ("env.NAME", "vault.path", or the object forms of
// those). Bifrost would substitute the referenced value and send it to the configured
// provider URL, which would let an administrator exfiltrate the server's own secrets.
func rejectSecretReferences(body []byte) error {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		// Not JSON; Bifrost will reject it on its own.
		return nil
	}
	var walk func(v any) error
	walk = func(v any) error {
		switch v := v.(type) {
		case string:
			if strings.HasPrefix(v, "env.") || strings.HasPrefix(v, "vault.") {
				return types.NewErrBadRequest("environment and vault references (%q) are not supported; enter the value itself", v)
			}
		case map[string]any:
			if t, _ := v["type"].(string); t == "env" || t == "vault" {
				return types.NewErrBadRequest("environment and vault references are not supported; enter the value itself")
			}
			if fromEnv, _ := v["from_env"].(bool); fromEnv {
				return types.NewErrBadRequest("environment references are not supported; enter the value itself")
			}
			for _, child := range v {
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(doc)
}

// afterMutation keeps Obot in step with changes made through the proxy.
func (h *LLMGatewayHandler) afterMutation(ctx context.Context, req api.Context, path string) {
	switch {
	case strings.HasPrefix(path, "providers"), strings.HasPrefix(path, "models"), strings.HasPrefix(path, "keys"):
		if strings.HasPrefix(path, "providers") && req.Method != http.MethodDelete {
			h.engine.ApplyEgressProxy(ctx)
		}
		if err := bifrost.TriggerModelSync(ctx, req.Storage); err != nil {
			slog.Warn("failed to trigger LLM gateway model sync", "error", err)
		}
	}
}

// announceLLMGatewayChange advances the gateway's configuration revision, so the other
// replicas reload their gateways from the shared stores.
func announceLLMGatewayChange(storage kclient.Client) func(context.Context) (int64, error) {
	return func(ctx context.Context) (int64, error) {
		return providerconfigurationchange.AdvanceLLMGatewayRevision(ctx, storage)
	}
}

// Metrics serves the gateway's Prometheus metrics: request counts, latencies, tokens and cost
// by provider, model and key.
func (h *LLMGatewayHandler) Metrics(req api.Context) error {
	if err := h.requireEngine(); err != nil {
		return err
	}
	out, err := http.NewRequestWithContext(req.Context(), http.MethodGet, "/metrics", nil)
	if err != nil {
		return err
	}
	rec := httptest.NewRecorder()
	h.engine.ServeHTTP(rec, out)
	// Publish the metrics under Obot's own name.
	metrics := strings.ReplaceAll(rec.Body.String(), "bifrost_", "obot_llm_gateway_")
	req.ResponseWriter.Header().Set("Content-Type", rec.Header().Get("Content-Type"))
	req.WriteHeader(rec.Code)
	_, err = req.ResponseWriter.Write([]byte(metrics))
	return err
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

// GetSettings returns the LLM gateway settings Obot enforces.
func (h *LLMGatewayHandler) GetSettings(req api.Context) error {
	settings, err := req.GatewayClient.GetLLMGatewaySettings(req.Context())
	if err != nil {
		return err
	}
	return req.Write(settings)
}

// UpdateSettings replaces the LLM gateway settings Obot enforces.
func (h *LLMGatewayHandler) UpdateSettings(req api.Context) error {
	var settings gatewaytypes.LLMGatewaySettings
	if err := req.Read(&settings); err != nil {
		return types.NewErrBadRequest("invalid settings: %v", err)
	}
	if err := req.GatewayClient.SetLLMGatewaySettings(req.Context(), settings); err != nil {
		return err
	}
	return req.Write(settings)
}

func (h *LLMGatewayHandler) ListBudgets(req api.Context) error {
	budgets, err := req.GatewayClient.ListLLMGatewayBudgets(req.Context())
	if err != nil {
		return err
	}
	if budgets == nil {
		budgets = []gatewaytypes.LLMGatewayBudget{}
	}
	return req.Write(map[string]any{"items": budgets})
}

func (h *LLMGatewayHandler) GetBudget(req api.Context) error {
	budget, err := h.getBudget(req)
	if err != nil {
		return err
	}
	return req.Write(budget)
}

func (h *LLMGatewayHandler) getBudget(req api.Context) (*gatewaytypes.LLMGatewayBudget, error) {
	budget, err := req.GatewayClient.GetLLMGatewayBudget(req.Context(), req.PathValue("budget_id"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, types.NewErrNotFound("budget %q not found", req.PathValue("budget_id"))
	}
	return budget, err
}

func (h *LLMGatewayHandler) CreateBudget(req api.Context) error {
	var budget gatewaytypes.LLMGatewayBudget
	if err := req.Read(&budget); err != nil {
		return types.NewErrBadRequest("invalid budget: %v", err)
	}
	if err := req.GatewayClient.CreateLLMGatewayBudget(req.Context(), &budget); err != nil {
		return types.NewErrBadRequest("%v", err)
	}
	return req.WriteCreated(budget)
}

func (h *LLMGatewayHandler) UpdateBudget(req api.Context) error {
	existing, err := h.getBudget(req)
	if err != nil {
		return err
	}
	var budget gatewaytypes.LLMGatewayBudget
	if err := req.Read(&budget); err != nil {
		return types.NewErrBadRequest("invalid budget: %v", err)
	}
	budget.ID = existing.ID
	if err := req.GatewayClient.UpdateLLMGatewayBudget(req.Context(), &budget); err != nil {
		return types.NewErrBadRequest("%v", err)
	}
	updated, err := h.getBudget(req)
	if err != nil {
		return err
	}
	return req.Write(updated)
}

func (h *LLMGatewayHandler) DeleteBudget(req api.Context) error {
	if _, err := h.getBudget(req); err != nil {
		return err
	}
	if err := req.GatewayClient.DeleteLLMGatewayBudget(req.Context(), req.PathValue("budget_id")); err != nil {
		return err
	}
	req.WriteHeader(http.StatusNoContent)
	return nil
}

// BudgetUsage reports usage in each limit's current window: per user for per-user budgets,
// or one total for shared budgets.
func (h *LLMGatewayHandler) BudgetUsage(req api.Context) error {
	budget, err := h.getBudget(req)
	if err != nil {
		return err
	}
	entries, err := req.GatewayClient.LLMGatewayBudgetUsageReport(req.Context(), budget)
	if err != nil {
		return err
	}
	return req.Write(map[string]any{"items": entries})
}

// ImportLegacyProviders copies the credentials of configured legacy model providers into
// the gateway, so an existing installation can switch over without re-entering keys.
func (h *LLMGatewayHandler) ImportLegacyProviders(req api.Context) error {
	if err := h.requireEngine(); err != nil {
		return err
	}
	var (
		results   []bifrost.LegacyImportResult
		importErr error
	)
	if err := h.engine.Change(req.Context(), func() (bool, bool) {
		results, importErr = h.engine.ImportLegacyModelProviders(req.Context(), req.Storage, req.GatewayClient)
		if importErr != nil {
			// Some providers may have been imported before the error; reload everywhere.
			return true, false
		}
		h.engine.ApplyEgressProxy(req.Context())
		return true, true
	}, announceLLMGatewayChange(req.Storage)); err != nil {
		return types.NewErrHTTP(http.StatusInternalServerError, err.Error())
	}
	if importErr != nil {
		return importErr
	}
	if err := bifrost.TriggerModelSync(req.Context(), req.Storage); err != nil {
		slog.Warn("failed to trigger LLM gateway model sync", "error", err)
	}
	return req.Write(map[string]any{"items": results})
}
