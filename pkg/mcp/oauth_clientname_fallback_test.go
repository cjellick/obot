package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientNameCandidates(t *testing.T) {
	t.Run("primary only when no fallbacks configured", func(t *testing.T) {
		t.Setenv("OBOT_MCP_OAUTH_CLIENT_NAME_FALLBACKS", "")
		assert.Equal(t, []string{"Obot MCP OAuth"}, clientNameCandidates("Obot MCP OAuth"))
	})

	t.Run("primary then fallbacks, de-duplicated and trimmed", func(t *testing.T) {
		t.Setenv("OBOT_MCP_OAUTH_CLIENT_NAME_FALLBACKS", " Claude Code , Cursor ,Obot MCP OAuth")
		// "Obot MCP OAuth" is dropped from the fallbacks because it equals the primary.
		assert.Equal(t, []string{"Obot MCP OAuth", "Claude Code", "Cursor"},
			clientNameCandidates("Obot MCP OAuth"))
	})

	t.Run("fallbacks still tried when primary is empty", func(t *testing.T) {
		t.Setenv("OBOT_MCP_OAUTH_CLIENT_NAME_FALLBACKS", "Claude Code")
		assert.Equal(t, []string{"Claude Code"}, clientNameCandidates(""))
	})
}

// TestResolveClientInfoFallsBackToAllowlistedName simulates an authorization
// server (like Figma) that 403s every client_name except an allowlisted one,
// and verifies the DCR loop falls through to a configured fallback name.
func TestResolveClientInfoFallsBackToAllowlistedName(t *testing.T) {
	const allowlisted = "Claude Code"

	var attempted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var reg struct {
			ClientName string `json:"client_name"`
		}
		_ = json.Unmarshal(body, &reg)
		attempted = append(attempted, reg.ClientName)

		if reg.ClientName != allowlisted {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Forbidden"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":     "issued-for-" + reg.ClientName,
			"client_secret": "secret",
		})
	}))
	defer srv.Close()

	discovery := oauthMetadataDiscovery{
		ProtectedResourceMetadata: protectedResourceMetadata{
			AuthorizationServers: []string{"https://as.example.com"},
		},
		AuthorizationServerMetadata: AuthorizationServerMetadata{
			RegistrationEndpoint: srv.URL,
		},
		ClientRegistration: ClientRegistrationMetadata{
			ClientName:   "Obot MCP OAuth",
			RedirectURIs: []string{"https://obot.example.com/oauth/mcp/callback"},
		},
	}

	o := &oauth{metadataClient: srv.Client()}

	t.Run("without fallbacks the primary rejection is returned", func(t *testing.T) {
		t.Setenv("OBOT_MCP_OAUTH_CLIENT_NAME_FALLBACKS", "")
		attempted = nil
		_, err := o.resolveClientInfo(context.Background(), "srv", discovery)
		require.Error(t, err)
		assert.Equal(t, []string{"Obot MCP OAuth"}, attempted)
	})

	t.Run("falls back to the allowlisted name", func(t *testing.T) {
		t.Setenv("OBOT_MCP_OAUTH_CLIENT_NAME_FALLBACKS", allowlisted)
		attempted = nil
		info, err := o.resolveClientInfo(context.Background(), "srv", discovery)
		require.NoError(t, err)
		assert.Equal(t, "issued-for-"+allowlisted, info.ClientID)
		// Tried the primary first, then fell back.
		assert.Equal(t, []string{"Obot MCP OAuth", allowlisted}, attempted)
	})
}
