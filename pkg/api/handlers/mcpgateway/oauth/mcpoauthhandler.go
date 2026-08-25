package oauth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/obot-platform/obot/pkg/mcp"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	"golang.org/x/oauth2"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type MCPOAuthHandlerFactory struct {
	baseURL                   string
	mcpSessionManager         *mcp.SessionManager
	client                    kclient.Client
	stateMgr                  *stateManager
	tokenStore                mcp.GlobalTokenStore
	secretBindingAllowedLabel string
	cimdDocumentURL           string

	// resolveOAuthClient resolves a downstream client_id (either a stored
	// "namespace:name" dynamic-registration client or a client ID metadata
	// document URL) to its OAuthClient record. It is injected by SetupHandlers
	// from the handler, which owns the SSRF-safe metadata-document fetcher and
	// cache. Used to forward the connecting client's name to upstream DCR.
	resolveOAuthClient func(ctx context.Context, c kclient.Client, clientID string) (v1.OAuthClient, error)
}

type mcpOAuthHandler struct {
	gatewayClient      *client.Client
	stateMgr           *stateManager
	mcpID              string
	mcpURL             string
	userID             string
	oauthAuthRequestID string
	urlChan            chan string

	// catalogEntryName is the name of the catalog entry to fetch static OAuth credentials for.
	catalogEntryName string
}

func NewMCPOAuthHandlerFactory(baseURL string, sessionManager *mcp.SessionManager, client kclient.Client, gatewayClient *client.Client, globalTokenStore mcp.GlobalTokenStore, secretBindingAllowedLabel string, forceDynamicClient bool) *MCPOAuthHandlerFactory {
	f := &MCPOAuthHandlerFactory{
		baseURL:                   baseURL,
		mcpSessionManager:         sessionManager,
		client:                    client,
		stateMgr:                  newStateManager(gatewayClient),
		tokenStore:                globalTokenStore,
		secretBindingAllowedLabel: secretBindingAllowedLabel,
	}

	if !forceDynamicClient && strings.HasPrefix(baseURL, "https://") {
		f.cimdDocumentURL = system.OAuthClientIDMetadataURL(baseURL)
	}

	return f
}

func (f *MCPOAuthHandlerFactory) CheckForMCPAuth(req api.Context, mcpServer v1.MCPServer, mcpServerConfig mcp.ServerConfig, userID, mcpID, oauthAppAuthRequestID string) (string, error) {
	if mcpServer.Spec.Manifest.Runtime == types.RuntimeComposite {
		var componentServers v1.MCPServerList
		if err := f.client.List(req.Context(), &componentServers,
			kclient.InNamespace(mcpServer.Namespace),
			kclient.MatchingFields{"spec.compositeName": mcpServer.Name},
		); err != nil {
			return "", fmt.Errorf("failed to list component servers")
		}

		// Precompute disabled component set for quick lookup (by catalog entry ID only)
		var compositeConfig types.CompositeRuntimeConfig
		if mcpServer.Spec.Manifest.CompositeConfig != nil {
			compositeConfig = *mcpServer.Spec.Manifest.CompositeConfig
		}

		disabled := make(map[string]bool, len(compositeConfig.ComponentServers))
		for _, comp := range compositeConfig.ComponentServers {
			disabled[comp.CatalogEntryID] = comp.Disabled
		}

		for _, componentServer := range componentServers.Items {
			// Skip disabled components defined in the composite server config using O(1) lookups
			if disabled[componentServer.Spec.MCPServerCatalogEntryName] ||
				componentServer.Spec.Manifest.Runtime != types.RuntimeRemote {
				continue
			}

			_, componentConfig, err := f.mcpSessionManager.ServerForAction(req.Context(), componentServer.Name, req.User.GetUID())
			if err != nil {
				continue
			}

			u, err := f.CheckForMCPAuth(req, componentServer, componentConfig, userID, componentServer.Name, oauthAppAuthRequestID)
			if err != nil {
				if req.Context().Err() != nil {
					return "", fmt.Errorf("failed to check component server OAuth: %w", req.Context().Err())
				}
				return "", fmt.Errorf("failed to check component server %s OAuth: %w", componentServer.Name, err)
			}

			if u != "" {
				// At least one component requires OAuth
				slog.Info("Composite MCP server requires component OAuth authentication", "compositeMCPID", mcpID, "componentMCPID", componentServer.Name)
				if oauthAppAuthRequestID != "" {
					return fmt.Sprintf("%s/auth/mcp/composite/%s?oauth_auth_request=%s", f.baseURL, mcpID, oauthAppAuthRequestID), nil
				}

				return fmt.Sprintf("%s/auth/mcp/composite/%s", f.baseURL, mcpID), nil
			}
		}

		// No component requires OAuth
		slog.Info("Composite MCP server passed OAuth check with no pending component authentication", "compositeMCPID", mcpID)
		return "", nil
	} else if mcpServerConfig.Runtime != types.RuntimeRemote {
		// Not a remote or composite server, no OAuth required
		return "", nil
	}

	if mcpServerConfig.TunnelName == "" {
		if err := mcp.ValidateRemoteMCPURL(req.Context(), mcpServerConfig.URL, f.mcpSessionManager.RemoteMCPURLValidationConfig()); err != nil {
			return "", err
		}
	}

	// Remote server, check for OAuth directly
	oauthHandler := f.newMCPOAuthHandler(req.GatewayClient, userID, mcpID, mcpServerConfig.URL, oauthAppAuthRequestID, mcpServerConfig.MCPCatalogEntryName)
	staticOAuthPending, err := f.staticOAuthPending(req.Context(), mcpServer, oauthHandler)
	if err != nil {
		return "", err
	}
	errChan := make(chan error, 1)

	go func() {
		defer close(errChan)

		_, err := f.mcpSessionManager.ClientForMCPServerForOAuthCheck(req.Context(), mcpServerConfig, mcp.ClientOption{
			ClientName:      "Obot MCP OAuth",
			OAuthClientName: f.downstreamOAuthClientName(req, oauthAppAuthRequestID),
			TokenStorage:    f.tokenStore.ForUserAndMCP(userID, mcpID, mcpServerConfig.URL),
			CallbackHandler: oauthHandler,
			ClientLookup:    oauthHandler,
		})
		if err != nil {
			errChan <- fmt.Errorf("failed to get client for server %s: %v", mcpServer.Name, err)
		} else {
			f.mcpSessionManager.CloseClient(mcpServerConfig, "Obot OAuth Check")
			errChan <- nil
		}
	}()

	select {
	case err := <-errChan:
		if err != nil || !staticOAuthPending {
			return "", err
		}
		return f.staticOAuthURL(req.Context(), mcpServerConfig, oauthHandler)
	case <-req.Context().Done():
		return "", fmt.Errorf("failed to check for MCP server OAuth: %w", req.Context().Err())
	case u := <-oauthHandler.URLChan():
		slog.Info("Remote MCP server requires OAuth authentication", "mcpID", mcpID)
		return u, nil
	}
}

// downstreamOAuthClientName resolves the client_name that the connecting client
// registered with Obot (via its own dynamic client registration), so it can be
// forwarded to the upstream server's dynamic client registration. Some upstreams
// (e.g. Figma) allowlist DCR by client_name; forwarding the real client's name
// makes the gateway transparent to that policy instead of registering under one
// fixed gateway identity. Returns "" when there is no downstream client context
// (e.g. UI-initiated flows) or it cannot be resolved, in which case the caller
// falls back to OBOT_MCP_OAUTH_CLIENT_NAME and then the static ClientName.
func (f *MCPOAuthHandlerFactory) downstreamOAuthClientName(req api.Context, oauthAuthRequestID string) string {
	if oauthAuthRequestID == "" || f.resolveOAuthClient == nil {
		return ""
	}

	var authReq v1.OAuthAuthRequest
	if err := req.Get(&authReq, oauthAuthRequestID); err != nil {
		return ""
	}
	if authReq.Spec.ClientID == "" {
		return ""
	}

	// resolveOAuthClient handles both stored "namespace:name" clients and
	// client ID metadata document URLs (e.g. Claude Code, which identifies via
	// https://claude.ai/oauth/claude-code-client-metadata), fetching the
	// document through the handler's SSRF-safe client.
	oauthClient, err := f.resolveOAuthClient(req.Context(), req.Storage, authReq.Spec.ClientID)
	if err != nil {
		return ""
	}

	return oauthClient.Spec.Manifest.ClientName
}

func (f *MCPOAuthHandlerFactory) staticOAuthPending(ctx context.Context, mcpServer v1.MCPServer, oauthHandler *mcpOAuthHandler) (bool, error) {
	if !mcp.RequiresStaticOAuth(mcpServer) {
		return false, nil
	}

	conf, token, err := f.tokenStore.ForUserAndMCP(oauthHandler.userID, oauthHandler.mcpID, oauthHandler.mcpURL).GetTokenConfig(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to check stored OAuth token for MCP server %s: %w", mcpServer.Name, err)
	}
	return conf == nil || token == nil || token.AccessToken == "", nil
}

func (f *MCPOAuthHandlerFactory) staticOAuthURL(ctx context.Context, serverConfig mcp.ServerConfig, oauthHandler *mcpOAuthHandler) (string, error) {
	metadata, err := f.mcpSessionManager.GetOAuthMetadata(ctx, serverConfig,
		"Obot MCP Gateway", system.MCPOAuthCallbackURL(f.baseURL), true)
	if err != nil {
		return "", fmt.Errorf("failed to discover OAuth metadata for static OAuth server: %w", err)
	}

	callbackURL := system.MCPOAuthCallbackURL(f.baseURL)
	authorizationServer, registration, err := staticOAuthMetadata(metadata, callbackURL)
	if err != nil {
		return "", err
	}

	clientID, clientSecret, err := oauthHandler.Lookup(ctx)
	if err != nil {
		return "", err
	}
	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  callbackURL,
		Endpoint: oauth2.Endpoint{
			AuthURL:   authorizationServer.AuthorizationEndpoint,
			TokenURL:  authorizationServer.TokenEndpoint,
			AuthStyle: staticOAuthAuthStyle(registration.TokenEndpointAuthMethod),
		},
	}
	if registration.Scope != "" {
		conf.Scopes = strings.Fields(registration.Scope)
	}

	resourceURL := mcp.ResolveOAuthResourceURL(authorizationServer.AuthorizationEndpoint, metadata.ResourceURL, serverConfig.URL)
	authURL, _, _, err := mcp.GetOAuthAuthorizationURL(ctx, oauthHandler, conf, authorizationServer.AuthorizationEndpoint, resourceURL)
	if err != nil {
		return "", err
	}
	slog.Info("Remote MCP server requires configured static OAuth authentication", "mcpID", oauthHandler.mcpID)
	return authURL, nil
}

func staticOAuthMetadata(metadata mcp.OAuthMetadata, redirectURL string) (mcp.AuthorizationServerMetadata, mcp.ClientRegistrationMetadata, error) {
	var authorizationServer mcp.AuthorizationServerMetadata
	if len(metadata.AuthorizationServerMetadata) > 0 {
		if err := json.Unmarshal(metadata.AuthorizationServerMetadata, &authorizationServer); err != nil {
			return authorizationServer, mcp.ClientRegistrationMetadata{}, fmt.Errorf("failed to parse authorization server metadata: %w", err)
		}
	}
	if authorizationServer.AuthorizationEndpoint == "" || authorizationServer.TokenEndpoint == "" {
		return authorizationServer, mcp.ClientRegistrationMetadata{}, fmt.Errorf("static OAuth is required but authorization server metadata was not found")
	}

	var registration mcp.ClientRegistrationMetadata
	if len(metadata.ClientRegistration) > 0 {
		if err := json.Unmarshal(metadata.ClientRegistration, &registration); err != nil {
			return authorizationServer, registration, fmt.Errorf("failed to parse OAuth client registration metadata: %w", err)
		}
	}

	return authorizationServer, mcp.AuthServerMetadataToClientRegistration(authorizationServer,
		"Obot MCP Gateway", redirectURL, registration.Scope), nil
}

func staticOAuthAuthStyle(method string) oauth2.AuthStyle {
	switch method {
	case "client_secret_basic":
		return oauth2.AuthStyleInHeader
	case "client_secret_post":
		return oauth2.AuthStyleInParams
	default:
		return oauth2.AuthStyleAutoDetect
	}
}

func (f *MCPOAuthHandlerFactory) newMCPOAuthHandler(gatewayClient *client.Client, userID, mcpID, mcpURL, oauthAuthRequestID, catalogEntryName string) *mcpOAuthHandler {
	return &mcpOAuthHandler{
		gatewayClient:      gatewayClient,
		stateMgr:           f.stateMgr,
		userID:             userID,
		mcpID:              mcpID,
		mcpURL:             mcpURL,
		oauthAuthRequestID: oauthAuthRequestID,
		catalogEntryName:   catalogEntryName,
		urlChan:            make(chan string, 1),
	}
}

func (m *mcpOAuthHandler) URLChan() <-chan string {
	return m.urlChan
}

func (m *mcpOAuthHandler) HandleAuthURL(ctx context.Context, _ string, authURL string) (bool, error) {
	select {
	case m.urlChan <- authURL:
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	default:
		return false, nil
	}
}

func (m *mcpOAuthHandler) NewState(ctx context.Context, conf *oauth2.Config, resourceURL, verifier string) (string, <-chan mcp.CallbackPayload, error) {
	state := strings.ToLower(rand.Text())

	// The channel is required by the nanobot CallbackHandler interface but is not used
	// in the Obot flow. The auth URL is handled via HandleAuthURL/URLChan, and the
	// callback arrives via a separate HTTP endpoint (oauthCallback) which looks up
	// the pending state from the DB directly.
	ch := make(chan mcp.CallbackPayload)
	return state, ch, m.stateMgr.store(ctx, m.userID, m.mcpID, m.mcpURL, m.oauthAuthRequestID, state, verifier, resourceURL, conf)
}

func (m *mcpOAuthHandler) Lookup(ctx context.Context) (string, string, error) {
	// If the server was created from a catalog entry, look up OAuth credentials by catalog entry name
	if m.catalogEntryName != "" {
		cred, err := m.gatewayClient.RevealCredential(ctx, []string{system.MCPOAuthCredentialName(m.catalogEntryName)}, system.StaticOAuthCredentialName)
		if err == nil {
			clientID := cred.Secrets["CLIENT_ID"]
			clientSecret := cred.Secrets["CLIENT_SECRET"]
			if clientID != "" && clientSecret != "" {
				return clientID, clientSecret, nil
			}
		}
	}

	return "", "", fmt.Errorf("no credentials found for MCP server %s", m.mcpID)
}
