package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	types "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/storage/scheme"
	"github.com/obot-platform/obot/pkg/system"
	"github.com/stretchr/testify/assert"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestDownstreamOAuthClientName verifies that the gateway resolves the
// client_name that the connecting client registered with Obot, so it can be
// forwarded to the upstream server's dynamic client registration. Resolution
// covers both classic dynamic-registration clients ("namespace:name") and
// client ID metadata document clients (a URL, e.g. Claude Code).
func TestDownstreamOAuthClientName(t *testing.T) {
	const (
		dcrAuthReq   = "oar1dcr"
		cimdAuthReq  = "oar1cimd"
		dcrClientID  = "default:oac1cursor"
		cimdClientID = "https://claude.ai/oauth/claude-code-client-metadata"
	)

	storage := clientfake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(
			&v1.OAuthAuthRequest{
				Namespace: system.DefaultNamespace,
				Name:      dcrAuthReq,
				Spec:      v1.OAuthAuthRequestSpec{ClientID: dcrClientID},
			},
			&v1.OAuthAuthRequest{
				Namespace: system.DefaultNamespace,
				Name:      cimdAuthReq,
				Spec:      v1.OAuthAuthRequestSpec{ClientID: cimdClientID},
			},
		).
		Build()

	// Stand in for handler.resolveOAuthClient: a DCR client resolves to a
	// stored name; a CIMD URL resolves to the name from its (fetched) document.
	resolver := func(_ context.Context, _ kclient.Client, clientID string) (v1.OAuthClient, error) {
		switch clientID {
		case dcrClientID:
			return oauthClientWithName("Cursor"), nil
		case cimdClientID:
			return oauthClientWithName("Claude Code"), nil
		default:
			return v1.OAuthClient{}, fmt.Errorf("client_id does not exist: %s", clientID)
		}
	}

	f := &MCPOAuthHandlerFactory{resolveOAuthClient: resolver}
	req := api.Context{
		Request: httptest.NewRequest(http.MethodGet, "/oauth/callback", nil),
		Storage: storage,
	}

	t.Run("resolves dynamic-registration client name", func(t *testing.T) {
		assert.Equal(t, "Cursor", f.downstreamOAuthClientName(req, dcrAuthReq))
	})

	t.Run("resolves client ID metadata document (CIMD) client name", func(t *testing.T) {
		// This is the Claude Code case that the first cut of the fix missed:
		// CIMD clients must resolve to their document's client_name, not fall back.
		assert.Equal(t, "Claude Code", f.downstreamOAuthClientName(req, cimdAuthReq))
	})

	t.Run("empty auth request id falls back", func(t *testing.T) {
		assert.Equal(t, "", f.downstreamOAuthClientName(req, ""))
	})

	t.Run("missing auth request falls back", func(t *testing.T) {
		assert.Equal(t, "", f.downstreamOAuthClientName(req, "does-not-exist"))
	})

	t.Run("nil resolver falls back", func(t *testing.T) {
		f := &MCPOAuthHandlerFactory{}
		assert.Equal(t, "", f.downstreamOAuthClientName(req, dcrAuthReq))
	})
}

func oauthClientWithName(name string) v1.OAuthClient {
	return v1.OAuthClient{
		Spec: v1.OAuthClientSpec{
			Manifest: types.OAuthClientManifest{ClientName: name},
		},
	}
}
