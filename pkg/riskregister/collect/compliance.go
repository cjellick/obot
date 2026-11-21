package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/riskregister"
	"github.com/obot-platform/obot/pkg/safehttp"
)

const (
	anthropicAPIURL = "https://api.anthropic.com"
	// complianceAPIVersion is the anthropic-version header the Compliance API docs use.
	complianceAPIVersion = "2026-10-21"
	compliancePageSize   = 1000
	// maxErrorBody bounds how much of an error response is kept in a source's status.
	maxErrorBody = 300

	activityMCPServerCreated = "mcp_server_created"
	activityIntegrationAdded = "integration_user_connected"
)

var (
	// complianceProducts maps the Activity Feed types this collector asks for to the product they
	// show use of. The feed has hundreds of types. These are the ones that say a person used a
	// product, as opposed to an administrator changing a setting.
	complianceProducts = map[string]complianceProduct{
		"claude_chat_created":            {Name: "Claude chat", Segment: types.RiskSegmentBusinessUser},
		"cowork_session_updated":         {Name: "Cowork", Segment: types.RiskSegmentBusinessUser},
		"claude_code_sign_in":            {Name: "Claude Code", Segment: types.RiskSegmentCodingAgent},
		"ccr_session_created":            {Name: "Claude Code", Segment: types.RiskSegmentCodingAgent},
		"platform_agent_session_created": {Name: "Claude Platform agents", Segment: types.RiskSegmentAPIWorkload},
	}
)

// ClaudeCompliance reads the Activity Feed of Anthropic's Compliance API: who used which
// Claude product, and which connectors people attached inside Claude. It reads event
// metadata only, never chat or session content.
type ClaudeCompliance struct {
	APIKey string
	// BaseURL overrides the API address in tests.
	BaseURL string
	Client  *http.Client
}

type complianceProduct struct {
	Name    string
	Segment types.RiskSegment
}

type compliancePage struct {
	Data    []map[string]any `json:"data"`
	HasMore bool             `json:"has_more"`
	LastID  string           `json:"last_id"`
}

type productTally struct {
	product  complianceProduct
	users    map[string]bool
	events   int
	lastSeen time.Time
}

type connectorTally struct {
	name   string
	url    string
	users  map[string]bool
	events int
}

func (*ClaudeCompliance) Kind() types.RiskSourceKind {
	return types.RiskSourceClaudeCompliance
}

func (c *ClaudeCompliance) Collect(ctx context.Context, snapshot *riskregister.Snapshot) (map[string]int, error) {
	query := url.Values{}
	query.Set("limit", strconv.Itoa(compliancePageSize))
	query.Set("created_at.gte", windowStart(snapshot).UTC().Format(time.RFC3339))
	for activity := range complianceProducts {
		query.Add("activity_types[]", activity)
	}
	query.Add("activity_types[]", activityMCPServerCreated)
	query.Add("activity_types[]", activityIntegrationAdded)

	var (
		products   = map[string]*productTally{}
		connectors = map[string]*connectorTally{}
		events     int
	)
	for range maxPages {
		result, err := c.page(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, activity := range result.Data {
			events++
			kind, _ := activity["type"].(string)
			actor, _ := activity["actor"].(map[string]any)
			user := strings.ToLower(stringField(actor, "email_address"))
			seen, _ := time.Parse(time.RFC3339, stringField(activity, "created_at"))

			if product, ok := complianceProducts[kind]; ok {
				if user == "" {
					continue
				}
				t := products[product.Name]
				if t == nil {
					t = &productTally{product: product, users: map[string]bool{}}
					products[product.Name] = t
				}
				t.users[user] = true
				t.events++
				if seen.After(t.lastSeen) {
					t.lastSeen = seen
				}
				continue
			}

			name, serverURL := connectorIdentity(kind, activity)
			if name == "" {
				continue
			}
			key := strings.ToLower(name + "|" + serverURL)
			t := connectors[key]
			if t == nil {
				t = &connectorTally{name: name, url: serverURL, users: map[string]bool{}}
				connectors[key] = t
			}
			t.events++
			if user != "" {
				t.users[user] = true
			}
		}
		if !result.HasMore || result.LastID == "" {
			break
		}
		query.Set("after_id", result.LastID)
	}

	for _, t := range products {
		snapshot.Activity = append(snapshot.Activity, riskregister.ActivityObservation{
			Source:   types.RiskSourceClaudeCompliance,
			Product:  t.product.Name,
			Segment:  t.product.Segment,
			Users:    sortedKeys(t.users),
			Events:   t.events,
			LastSeen: t.lastSeen,
		})
	}
	sort.Slice(snapshot.Activity, func(i, j int) bool { return snapshot.Activity[i].Product < snapshot.Activity[j].Product })
	for _, t := range connectors {
		snapshot.Connectors = append(snapshot.Connectors, riskregister.ConnectorObservation{
			Source: types.RiskSourceClaudeCompliance,
			Name:   t.name,
			URL:    t.url,
			Users:  len(t.users),
			Events: t.events,
		})
	}
	sort.Slice(snapshot.Connectors, func(i, j int) bool { return snapshot.Connectors[i].Name < snapshot.Connectors[j].Name })

	return map[string]int{"events": events, "products": len(products), "connectors": len(connectors)}, nil
}

func (c *ClaudeCompliance) page(ctx context.Context, query url.Values) (*compliancePage, error) {
	base := c.BaseURL
	if base == "" {
		base = anthropicAPIURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/compliance/activities?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", complianceAPIVersion)

	client := c.Client
	if client == nil {
		client = safehttp.NewClient(outsidePolicy)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError("Compliance API", resp)
	}
	var result compliancePage
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to read the Compliance API response: %w", err)
	}
	return &result, nil
}

// connectorIdentity names the connector an activity is about. The feed's per-type fields are
// read loosely, so a field Anthropic renames or adds doesn't break collection.
func connectorIdentity(kind string, activity map[string]any) (name, serverURL string) {
	switch kind {
	case activityIntegrationAdded:
		return stringField(activity, "integration_name"), ""
	case activityMCPServerCreated:
		serverURL = stringField(activity, "mcp_server_url")
		for _, field := range []string{"mcp_server_name", "name"} {
			if name = stringField(activity, field); name != "" {
				return name, serverURL
			}
		}
		if parsed, err := url.Parse(serverURL); err == nil && parsed.Hostname() != "" {
			return parsed.Hostname(), serverURL
		}
	}
	return "", ""
}

func stringField(object map[string]any, field string) string {
	value, _ := object[field].(string)
	return value
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// statusError describes a failed response without keeping more of its body than is useful
// in a status line.
func statusError(service string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return fmt.Errorf("%s returned %s: %s", service, resp.Status, strings.TrimSpace(string(body)))
}
