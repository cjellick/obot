package collect

import (
	"context"
	"fmt"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/riskregister"
	"github.com/obot-platform/obot/pkg/safehttp"
)

const (
	providerAnthropic     = "Anthropic"
	anthropicKeysPageSize = 1000
	anthropicKeyActive    = "active"
)

// AnthropicAdmin lists an Anthropic organization's API keys through the Admin API. It reads
// key metadata only. The key values themselves are never available.
type AnthropicAdmin struct {
	// AdminKey is an Admin API key (sk-ant-admin...).
	AdminKey string
	// BaseURL overrides the API address in tests.
	BaseURL string
	Client  *http.Client
}

func (*AnthropicAdmin) Kind() types.RiskSourceKind {
	return types.RiskSourceAnthropicAdmin
}

func (a *AnthropicAdmin) Collect(ctx context.Context, snapshot *riskregister.Snapshot) (map[string]int, error) {
	httpClient := a.Client
	if httpClient == nil {
		httpClient = safehttp.NewClient(outsidePolicy)
	}
	// The credential comes from the source's own configuration. Nothing is taken from the
	// server's environment.
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(a.AdminKey),
		option.WithHTTPClient(httpClient),
	}
	if a.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(a.BaseURL))
	}
	client := anthropic.NewClient(opts...)

	var total, active int
	keys := client.Beta.Organization.APIKeys.ListAutoPaging(ctx, anthropic.BetaOrganizationAPIKeyListParams{Limit: anthropic.Int(anthropicKeysPageSize)})
	for keys.Next() {
		key := keys.Current()
		total++
		isActive := string(key.Status) == anthropicKeyActive
		if isActive {
			active++
		}
		scope := "all workspaces"
		if key.Scope.WorkspaceID != "" {
			scope = "workspace " + key.Scope.WorkspaceID
		}
		snapshot.APIKeys = append(snapshot.APIKeys, riskregister.APIKeyObservation{
			Source:    types.RiskSourceAnthropicAdmin,
			Provider:  providerAnthropic,
			ID:        key.ID,
			Name:      key.Name,
			Active:    isActive,
			Scope:     scope,
			CreatedAt: key.CreatedAt,
		})
	}
	if err := keys.Err(); err != nil {
		return nil, fmt.Errorf("failed to list Anthropic API keys: %w", err)
	}
	return map[string]int{"apiKeys": total, "activeKeys": active}, nil
}
