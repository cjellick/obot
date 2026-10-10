package server

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	types2 "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/bifrost"
	"github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/obot-platform/obot/pkg/guardrails"
	llmtypes "github.com/obot-platform/obot/pkg/llm"
	"github.com/obot-platform/obot/pkg/principal"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kuser "k8s.io/apiserver/pkg/authentication/user"
)

var (
	// clientGatewayHeaders are the request headers callers may use to tune the gateway for
	// their own requests, and the engine headers they map to. Everything else the engine reads
	// from headers (log metadata, direct keys, cache keys) is controlled by Obot.
	clientGatewayHeaders = map[string]string{
		"X-Obot-Cache-TTL":       "x-bf-cache-ttl",
		"X-Obot-Cache-Threshold": "x-bf-cache-threshold",
		"X-Obot-Cache-Type":      "x-bf-cache-type",
		"X-Obot-Cache-No-Store":  "x-bf-cache-no-store",
		"X-Obot-Session-ID":      "x-bf-session-id",
	}
)

// bifrostProviderBackend sends LLM proxy requests to the embedded Bifrost gateway. Obot
// still authenticates the caller, checks model access, enforces message policies and
// quotas, and records audit logs and usage; Bifrost then routes to the upstream provider
// with load balancing, fallbacks, governance, and its own request logging.
type bifrostProviderBackend struct {
	engine *bifrost.Engine
}

// bifrostRoute maps a client path under /api/llm-proxy/v1 to a Bifrost route.
type bifrostRoute struct {
	// base is the Bifrost path prefix the client path is joined to.
	base string
	// dialect is the API shape of the request and response.
	dialect llmtypes.Dialect
}

// sanitizingTransport strips implementation details from gateway responses before they
// reach clients: engine-specific headers (which include upstream key names), the engine's
// CORS headers, and the engine's extra response fields. The provider and model that served
// the request are passed on under Obot's own header names.
type sanitizingTransport struct {
	next http.RoundTripper
}

// sseSanitizer strips the engine's fields from each "data:" event of a stream, one line at a
// time so streaming isn't delayed.
type sseSanitizer struct {
	r       *bufio.Reader
	c       io.Closer
	pending []byte
}

// SetLLMGateway attaches the embedded Bifrost gateway, enabling the
// /api/llm-proxy/v1 routes.
func (s *Server) SetLLMGateway(engine *bifrost.Engine) {
	s.llmGateway = engine
}

// LLMGateway returns the embedded Bifrost gateway, or nil when it is disabled.
func (s *Server) LLMGateway() *bifrost.Engine {
	return s.llmGateway
}

func (s *Server) newBifrostLLMProviderProxy() *llmProviderProxy {
	return &llmProviderProxy{
		dailyUserInputTokenLimit:  s.dailyUserInputTokenLimit,
		dailyUserOutputTokenLimit: s.dailyUserOutputTokenLimit,
		backend:                   &bifrostProviderBackend{engine: s.llmGateway},
		mapHelper:                 s.mapHelper,
		// Guardrails, which include LLM-judge policies, run inside the gateway instead.
	}
}

func (b *bifrostProviderBackend) modelProviderName() string {
	return system.LLMGatewayModelProvider
}

// requiresCredential reports that the gateway has no Obot-managed credential: upstream
// keys live in Bifrost.
func (b *bifrostProviderBackend) requiresCredential() bool {
	return false
}

// resolveBifrostRoute decides which Bifrost route serves a client path. Only model-bearing
// inference endpoints and model listing are exposed, because Obot authorizes each request
// by the model it names.
func resolveBifrostRoute(req *http.Request) (bifrostRoute, error) {
	reqPath := strings.Trim(req.PathValue("path"), "/")
	endpoint, hasV1 := strings.CutPrefix(reqPath, "v1/")
	if !hasV1 {
		return bifrostRoute{}, types2.NewErrBadRequest("unsupported LLM gateway path %q: paths must start with v1/", reqPath)
	}

	switch endpoint {
	case "messages", "messages/count_tokens":
		return bifrostRoute{base: "/anthropic", dialect: llmtypes.DialectAnthropicMessages}, nil
	case "models":
		// Anthropic clients send anthropic-version on every request, including model
		// listing, and expect Anthropic's list format back.
		if req.Header.Get("anthropic-version") != "" {
			return bifrostRoute{base: "/anthropic", dialect: llmtypes.DialectAnthropicMessages}, nil
		}
		return bifrostRoute{base: "/"}, nil
	case "responses", "responses/input_tokens":
		return bifrostRoute{base: "/", dialect: llmtypes.DialectOpenAIResponses}, nil
	case "chat/completions":
		return bifrostRoute{base: "/", dialect: llmtypes.DialectOpenAIChatCompletions}, nil
	case "embeddings":
		// Embedding responses report usage in the Chat Completions shape.
		return bifrostRoute{base: "/", dialect: llmtypes.DialectOpenAIChatCompletions}, nil
	default:
		// Other request types (legacy completions, images, audio, rerank) aren't exposed until
		// Obot can meter them against daily token limits and apply message policies.
		return bifrostRoute{}, types2.NewErrBadRequest("unsupported LLM gateway endpoint %q", reqPath)
	}
}

func (b *bifrostProviderBackend) upstreamURL(req *http.Request, _ map[string]string) (url.URL, llmtypes.Dialect, error) {
	if b.engine == nil {
		return url.URL{}, "", types2.NewErrHTTP(http.StatusServiceUnavailable, "the LLM gateway is disabled")
	}
	if !b.engine.Ready() {
		if err := b.engine.Err(); err != nil {
			return url.URL{}, "", types2.NewErrHTTP(http.StatusServiceUnavailable, "the LLM gateway failed to start")
		}
		return url.URL{}, "", types2.NewErrHTTP(http.StatusServiceUnavailable, "the LLM gateway is starting")
	}
	route, err := resolveBifrostRoute(req)
	if err != nil {
		return url.URL{}, "", err
	}
	u := b.engine.BaseURL()
	u.Path = route.base
	return u, route.dialect, nil
}

func (b *bifrostProviderBackend) transport(v1.ModelProvider, map[string]string) (http.RoundTripper, error) {
	return sanitizingTransport{next: b.engine.Transport()}, nil
}

func (t sanitizingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	provider := resp.Header.Get("X-Bifrost-Provider")
	model := resp.Header.Get("X-Bifrost-Resolved-Model")
	for name := range resp.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-bifrost-") || strings.HasPrefix(lower, "x-bf-") ||
			strings.HasPrefix(lower, "access-control-") || lower == "server" || lower == "vary" {
			resp.Header.Del(name)
		}
	}
	if provider != "" {
		resp.Header.Set("X-Obot-LLM-Provider", provider)
	}
	if model != "" {
		resp.Header.Set("X-Obot-LLM-Model", model)
	}

	contentType := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(contentType, "text/event-stream"):
		resp.Body = &sseSanitizer{r: bufio.NewReader(resp.Body), c: resp.Body}
	case strings.Contains(contentType, "application/json"):
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		body = stripEngineFields(body)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	return resp, nil
}

// stripEngineFields removes the engine's own fields from a JSON response object.
func stripEngineFields(body []byte) []byte {
	if !gjson.ValidBytes(body) {
		return body
	}
	for _, field := range []string{"extra_fields", "is_bifrost_error"} {
		if gjson.GetBytes(body, field).Exists() {
			if stripped, err := sjson.DeleteBytes(body, field); err == nil {
				body = stripped
			}
		}
	}
	return body
}

func (s *sseSanitizer) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		line, err := s.r.ReadBytes('\n')
		if len(line) > 0 {
			if data, ok := bytes.CutPrefix(line, []byte("data: ")); ok && len(data) > 0 && data[0] == '{' {
				trimmed := bytes.TrimRight(data, "\r\n")
				line = append(append([]byte("data: "), stripEngineFields(trimmed)...), data[len(trimmed):]...)
			}
			s.pending = line
		}
		if err != nil {
			if len(s.pending) == 0 {
				return 0, err
			}
			break
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *sseSanitizer) Close() error {
	return s.c.Close()
}

// trackerDialect lets usage tracking follow the route: every model is served in every
// dialect, so the model's native dialect says nothing about the response shape.
func (b *bifrostProviderBackend) trackerDialect(routeDialect llmtypes.Dialect) llmtypes.Dialect {
	return routeDialect
}

// resolveModel falls back to matching a bare provider-native model ID against the
// gateway's provider-qualified target models.
func (b *bifrostProviderBackend) resolveModel(l *llmProviderProxy, reference string) (*v1.Model, error) {
	if strings.Contains(reference, "/") {
		return nil, nil
	}
	return l.mapHelper.ResolveProviderQualifiedModel(system.LLMGatewayModelProvider, reference)
}

// authorizeExtraModels checks every fallback model the request names. Bifrost tries
// fallbacks when the primary model fails, so an unchecked fallback would let a caller reach
// a model their access policy doesn't allow.
func (b *bifrostProviderBackend) authorizeExtraModels(l *llmProviderProxy, req api.Context, body []byte) ([]byte, error) {
	fallbacks := gjson.GetBytes(body, "fallbacks")
	if !fallbacks.Exists() {
		return body, nil
	}
	if !fallbacks.IsArray() {
		return nil, types2.NewErrBadRequest("fallbacks must be an array of model names")
	}

	agentModels, isAgent := principal.AuthorizedModelIDs(req.User)
	var resolved []string
	for _, f := range fallbacks.Array() {
		reference := f.String()
		if f.IsObject() {
			reference = f.Get("provider").String() + "/" + f.Get("model").String()
		}
		model, err := l.mapHelper.ResolveModelReference(req.Context(), req.Storage, system.DefaultNamespace, system.LLMGatewayModelProvider, reference)
		if apierrors.IsNotFound(err) {
			model, err = b.resolveModel(l, reference)
		}
		if err != nil || model == nil || model.Spec.Manifest.ModelProvider != system.LLMGatewayModelProvider {
			return nil, types2.NewErrBadRequest("fallback model %q is not available through this endpoint", reference)
		}
		if isAgent {
			if !modelAllowedForAgent(agentModels, model.Name) {
				return nil, types2.NewErrForbidden("agent is not configured to use fallback model %q", reference)
			}
		} else if ok, err := l.mapHelper.UserHasAccessToModel(req.User, model.Name); err != nil {
			return nil, err
		} else if !ok {
			return nil, types2.NewErrForbidden("user does not have permission to use fallback model %q", reference)
		}
		resolved = append(resolved, model.Spec.Manifest.TargetModel)
	}

	// Send Bifrost the resolved provider-qualified names.
	return sjson.SetBytes(body, "fallbacks", resolved)
}

// decorateRequest removes the caller's Obot credential and adds the identity headers
// Bifrost uses for request logs and caching, and the guardrails token.
func (b *bifrostProviderBackend) decorateRequest(ctx context.Context, gatewayClient *client.Client, req *http.Request, user kuser.Info, model *v1.Model) (func(), error) {
	for _, h := range []string{"Authorization", "X-Api-Key", "Api-Key", "X-Goog-Api-Key", "Cookie", guardrails.HeaderRequestToken} {
		req.Header.Del(h)
	}
	// Never let callers pick a virtual key or smuggle log metadata of their own.
	for name := range req.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-bf-") {
			req.Header.Del(name)
		}
	}
	for client, engine := range clientGatewayHeaders {
		if v := req.Header.Get(client); v != "" {
			req.Header.Set(engine, v)
			req.Header.Del(client)
		}
	}

	ownerID := principal.ResourceOwnerID(user)
	// Cache entries are scoped per user so one user's cached response is never served to
	// another. This only takes effect when an admin enables the cache.
	req.Header.Set(bifrost.HeaderCacheKey, "obot-user-"+ownerID)
	req.Header.Set(bifrost.HeaderLogUserID, ownerID)
	req.Header.Set(bifrost.HeaderDimUser, ownerID)
	if name := user.GetName(); name != "" {
		req.Header.Set(bifrost.HeaderLogUserName, name)
	}
	if model != nil {
		req.Header.Set(bifrost.HeaderLogModelID, model.Name)
		req.Header.Set(bifrost.HeaderDimObotModel, model.Spec.Manifest.Name)
	}

	// Tell the gateway's guardrail plugins who the request is for. Only a reference is sent;
	// the identity itself stays in this process.
	userID, groups, _, err := gatewayIdentity(ctx, gatewayClient, user)
	if err != nil {
		return nil, err
	}
	token, release := guardrails.BindRequest(guardrails.Identity{UserID: userID, Groups: groups})
	req.Header.Set(guardrails.HeaderRequestToken, token)
	return release, nil
}

// admit enforces LLM gateway budgets. It replaces the daily token quota for gateway traffic:
// budgets are enforced by Obot from the usage it records in its own database, so they hold
// exactly across replicas. A hosted agent spends its owner's budgets.
func (b *bifrostProviderBackend) admit(ctx context.Context, gatewayClient *client.Client, user kuser.Info, model *v1.Model) ([]string, error) {
	userID, groups, isAdmin, err := gatewayIdentity(ctx, gatewayClient, user)
	if err != nil {
		return nil, err
	}

	budgets, err := gatewayClient.LLMGatewayBudgetsForUser(ctx, userID, groups, isAdmin, model.Name)
	if err != nil {
		return nil, err
	}
	if err := gatewayClient.CheckLLMGatewayBudgets(ctx, budgets, userID); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(budgets))
	for _, budget := range budgets {
		ids = append(ids, budget.ID)
	}
	return ids, nil
}

// gatewayIdentity resolves whose budgets and guardrails apply to a request.
func gatewayIdentity(ctx context.Context, gatewayClient *client.Client, user kuser.Info) (userID string, groups []string, isAdmin bool, _ error) {
	identity, isAdmin, err := guardrails.ResolveIdentity(ctx, gatewayClient, user)
	return identity.UserID, identity.Groups, isAdmin, err
}
