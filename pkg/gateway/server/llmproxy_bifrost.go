package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	types2 "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/bifrost"
	"github.com/obot-platform/obot/pkg/gateway/client"
	llmtypes "github.com/obot-platform/obot/pkg/llm"
	"github.com/obot-platform/obot/pkg/principal"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	kuser "k8s.io/apiserver/pkg/authentication/user"
)

// SetLLMGateway attaches the embedded Bifrost gateway, enabling the
// /api/llm-proxy/bifrost routes.
func (s *Server) SetLLMGateway(engine *bifrost.Engine) {
	s.llmGateway = engine
}

// LLMGateway returns the embedded Bifrost gateway, or nil when it is disabled.
func (s *Server) LLMGateway() *bifrost.Engine {
	return s.llmGateway
}

// bifrostProviderBackend sends LLM proxy requests to the embedded Bifrost gateway. Obot
// still authenticates the caller, checks model access, enforces message policies and
// quotas, and records audit logs and usage; Bifrost then routes to the upstream provider
// with load balancing, fallbacks, governance, and its own request logging.
type bifrostProviderBackend struct {
	engine *bifrost.Engine
}

func (s *Server) newBifrostLLMProviderProxy() *llmProviderProxy {
	return &llmProviderProxy{
		dailyUserInputTokenLimit:  s.dailyUserInputTokenLimit,
		dailyUserOutputTokenLimit: s.dailyUserOutputTokenLimit,
		backend:                   &bifrostProviderBackend{engine: s.llmGateway},
		mapHelper:                 s.mapHelper,
		messagePolicyHelper:       s.messagePolicyHelper,
	}
}

func (b *bifrostProviderBackend) modelProviderName() string {
	return system.BifrostModelProvider
}

// requiresCredential reports that the gateway has no Obot-managed credential: upstream
// keys live in Bifrost.
func (b *bifrostProviderBackend) requiresCredential() bool {
	return false
}

// bifrostRoute maps a client path under /api/llm-proxy/bifrost to a Bifrost route.
type bifrostRoute struct {
	// base is the Bifrost path prefix the client path is joined to.
	base string
	// dialect is the API shape of the request and response.
	dialect llmtypes.Dialect
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

	switch {
	case endpoint == "messages" || endpoint == "messages/count_tokens":
		return bifrostRoute{base: "/anthropic", dialect: llmtypes.DialectAnthropicMessages}, nil
	case endpoint == "models":
		// Anthropic clients send anthropic-version on every request, including model
		// listing, and expect Anthropic's list format back.
		if req.Header.Get("anthropic-version") != "" {
			return bifrostRoute{base: "/anthropic", dialect: llmtypes.DialectAnthropicMessages}, nil
		}
		return bifrostRoute{base: "/"}, nil
	case endpoint == "responses" || endpoint == "responses/input_tokens" || endpoint == "responses/compact":
		return bifrostRoute{base: "/", dialect: llmtypes.DialectOpenAIResponses}, nil
	case endpoint == "chat/completions":
		return bifrostRoute{base: "/", dialect: llmtypes.DialectOpenAIChatCompletions}, nil
	case endpoint == "completions", endpoint == "embeddings", endpoint == "images/generations",
		endpoint == "audio/speech", endpoint == "rerank", endpoint == "moderations":
		return bifrostRoute{base: "/"}, nil
	default:
		return bifrostRoute{}, types2.NewErrBadRequest("unsupported LLM gateway endpoint %q", reqPath)
	}
}

func (b *bifrostProviderBackend) upstreamURL(req *http.Request, _ map[string]string) (url.URL, llmtypes.Dialect, error) {
	if b.engine == nil {
		return url.URL{}, "", types2.NewErrHTTP(http.StatusServiceUnavailable, "the LLM gateway is disabled")
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
	return b.engine.Transport(), nil
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
	return l.mapHelper.ResolveProviderQualifiedModel(system.BifrostModelProvider, reference)
}

// decorateRequest removes the caller's Obot credential and adds the identity and
// virtual-key headers Bifrost uses for logging, analytics, and governance.
func (b *bifrostProviderBackend) decorateRequest(ctx context.Context, gatewayClient *client.Client, req *http.Request, user kuser.Info, model *v1.Model) error {
	for _, h := range []string{"Authorization", "X-Api-Key", "Api-Key", "X-Goog-Api-Key", "Cookie"} {
		req.Header.Del(h)
	}
	// Never let callers pick a virtual key or smuggle log metadata of their own.
	for name := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-bf-") {
			req.Header.Del(name)
		}
	}

	userID := principal.ResourceOwnerID(user)
	req.Header.Set(bifrost.HeaderLogUserID, userID)
	req.Header.Set(bifrost.HeaderDimUser, userID)
	if name := user.GetName(); name != "" {
		req.Header.Set(bifrost.HeaderLogUserName, name)
	}
	if model != nil {
		req.Header.Set(bifrost.HeaderLogModelID, model.Name)
		req.Header.Set(bifrost.HeaderDimObotModel, model.Spec.Manifest.Name)
	}

	vkID, err := gatewayClient.LLMGatewayVirtualKeyForUser(ctx, userID, user.GetExtra()["auth_provider_groups"])
	if err != nil {
		return err
	}
	if vkID == "" {
		return nil
	}
	value, err := b.engine.VirtualKeyValue(ctx, vkID)
	if err != nil {
		// A binding that points at a deleted or disabled key must not silently lift the
		// caller's budget, so fail closed.
		slog.Warn("LLM gateway budget binding references an unusable virtual key", "virtualKeyID", vkID, "error", err)
		return types2.NewErrHTTP(http.StatusForbidden, "your LLM gateway budget is not available; contact an administrator")
	}
	req.Header.Set(bifrost.HeaderVirtualKey, value)
	return nil
}
