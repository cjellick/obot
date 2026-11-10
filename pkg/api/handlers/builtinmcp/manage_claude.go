package builtinmcp

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/obot-platform/obot/pkg/api"
	gtypes "github.com/obot-platform/obot/pkg/gateway/types"
)

// plumbingCategories are egress categories that are the session's machinery, not its work.
const plumbingCategories = "control-plane,runner,obot,model,git,mcp"

func (h *Handler) claudeEnvironments(ctx context.Context, req api.Context) ([]gtypes.ClaudeEnvironment, error) {
	var config struct {
		Enabled      bool                       `json:"enabled"`
		Environments []gtypes.ClaudeEnvironment `json:"environments"`
	}
	if err := h.callAPI(ctx, req, "GET", "/claude-environments", nil, nil, &config); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, fmt.Errorf("Claude environments are not enabled on this Obot")
	}
	return config.Environments, nil
}

// updateClaudeEnvironment reads an environment, applies change and saves it.
func (h *Handler) updateClaudeEnvironment(ctx context.Context, req api.Context, id string, change func(*gtypes.ClaudeEnvironment) error) (*mcp.CallToolResult, any, error) {
	environments, err := h.claudeEnvironments(ctx, req)
	if err != nil {
		return toolError(err)
	}
	i := slices.IndexFunc(environments, func(e gtypes.ClaudeEnvironment) bool { return e.ID == id })
	if i < 0 {
		return toolError(fmt.Errorf("no Claude environment %q; find IDs with list_claude_environments", id))
	}
	env := environments[i]
	if err := change(&env); err != nil {
		return toolError(err)
	}
	var saved gtypes.ClaudeEnvironment
	if err := h.callAPI(ctx, req, "PUT", "/claude-environments/"+url.PathEscape(id), nil, env, &saved); err != nil {
		return toolError(err)
	}
	return jsonResult(map[string]any{"environment": h.environmentView(ctx, req, saved), "message": "Saved. New sessions use the change."})
}

func (h *Handler) environmentView(ctx context.Context, req api.Context, env gtypes.ClaudeEnvironment) map[string]any {
	profiles := make([]map[string]any, 0, len(env.Profiles))
	for _, p := range env.Profiles {
		who := make([]string, 0, len(p.Subjects))
		for _, s := range p.Subjects {
			who = append(who, h.subjectLabel(ctx, req, s))
		}
		profiles = append(profiles, map[string]any{
			"name":           p.Name,
			"who":            who,
			"egressHosts":    p.EgressHosts,
			"allowAllEgress": p.AllowAllEgress,
			"allowClaudeMCP": p.AllowClaudeMCP,
			"mcpServerIDs":   p.MCPServerIDs,
		})
	}
	return map[string]any{
		"id":          env.ID,
		"name":        env.DisplayName,
		"egressHosts": env.Runtime.EgressHosts,
		"profiles":    profiles,
		"url":         h.serverURL + "/claude-environments",
	}
}

func (h *Handler) addClaudeEnvironmentTools(s *mcp.Server, req api.Context) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_claude_environments",
		Description: "List Claude environments: where Claude Code sessions run on Obot's infrastructure, with each " +
			"profile's users, allowed egress hosts and MCP access. Setting up a new environment needs a key from " +
			"claude.ai, which the administrator enters on the Environments page, not in chat.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		environments, err := h.claudeEnvironments(ctx, req)
		if err != nil {
			return toolError(err)
		}
		out := make([]map[string]any, 0, len(environments))
		for _, env := range environments {
			out = append(out, h.environmentView(ctx, req, env))
		}
		return jsonResult(map[string]any{"environments": out})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "update_claude_egress",
		Description: "Allow or stop allowing hosts that Claude environment sessions can reach, for one profile or the " +
			"whole environment. Confirm the hosts and where with the administrator first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in egressInput) (*mcp.CallToolResult, any, error) {
		return h.updateClaudeEnvironment(ctx, req, in.EnvironmentID, func(env *gtypes.ClaudeEnvironment) error {
			hosts := &env.Runtime.EgressHosts
			if in.Profile != "" {
				i := slices.IndexFunc(env.Profiles, func(p gtypes.ClaudeEnvironmentProfile) bool { return strings.EqualFold(p.Name, in.Profile) })
				if i < 0 {
					return fmt.Errorf("the environment has no profile %q", in.Profile)
				}
				hosts = &env.Profiles[i].EgressHosts
			}
			for _, host := range in.AllowHosts {
				if host = strings.ToLower(strings.TrimSpace(host)); host != "" && !slices.Contains(*hosts, host) {
					*hosts = append(*hosts, host)
				}
			}
			for _, host := range in.RemoveHosts {
				host = strings.ToLower(strings.TrimSpace(host))
				*hosts = slices.DeleteFunc(*hosts, func(h string) bool { return h == host })
			}
			return nil
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_claude_profile",
		Description: "Change a Claude environment profile: who it applies to, Claude MCP relay access, or unrestricted egress. Confirm with the administrator first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in profileSettingsInput) (*mcp.CallToolResult, any, error) {
		subjects, err := h.subjects(ctx, req, in.accessInput)
		if err != nil {
			return toolError(err)
		}
		return h.updateClaudeEnvironment(ctx, req, in.EnvironmentID, func(env *gtypes.ClaudeEnvironment) error {
			i := slices.IndexFunc(env.Profiles, func(p gtypes.ClaudeEnvironmentProfile) bool { return strings.EqualFold(p.Name, in.Profile) })
			if i < 0 {
				return fmt.Errorf("the environment has no profile %q", in.Profile)
			}
			p := &env.Profiles[i]
			if in.AllowClaudeMCP != nil {
				p.AllowClaudeMCP = *in.AllowClaudeMCP
			}
			if in.AllowAllEgress != nil {
				p.AllowAllEgress = *in.AllowAllEgress
			}
			for _, subject := range subjects {
				if !slices.Contains(p.Subjects, subject) {
					p.Subjects = append(p.Subjects, subject)
				}
			}
			return nil
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_blocked_egress",
		Description: "Hosts Claude environment sessions tried to reach and were denied, grouped by environment and host, " +
			"with counts, users and when it last happened. Use it to tell the administrator what was blocked and offer " +
			"to allow a host with update_claude_egress.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in blockedEgressInput) (*mcp.CallToolResult, any, error) {
		blocked, err := h.blockedEgress(ctx, req, in)
		if err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{
			"blocked": blocked,
			"next": "Tell the administrator what was blocked and offer to allow hosts that look legitimate with update_claude_egress.",
		})
	})
}

type blockedHost struct {
	EnvironmentID string    `json:"environmentID"`
	Environment   string    `json:"environment"`
	Host          string    `json:"host"`
	Count         int       `json:"count"`
	Users         []string  `json:"users"`
	LastSeen      time.Time `json:"lastSeen"`
	Example       string    `json:"example"`
}

func (h *Handler) blockedEgress(ctx context.Context, req api.Context, in blockedEgressInput) ([]blockedHost, error) {
	days := in.Days
	if days <= 0 {
		days = 7
	}
	since := time.Now().Add(-time.Duration(min(days, 90)) * 24 * time.Hour)
	q := url.Values{"outcome": {"denied"}, "exclude_category": {plumbingCategories}, "limit": {"500"}}
	if in.EnvironmentID != "" {
		q.Set("environment_id", in.EnvironmentID)
	}
	var page struct {
		Items []gtypes.EgressAuditLog `json:"items"`
	}
	if err := h.callAPI(ctx, req, "GET", "/egress-audit-logs", q, nil, &page); err != nil {
		return nil, err
	}
	names := map[string]string{}
	if environments, err := h.claudeEnvironments(ctx, req); err == nil {
		for _, e := range environments {
			names[e.ID] = e.DisplayName
		}
	}
	groups := map[string]*blockedHost{}
	for _, e := range page.Items {
		if e.CreatedAt.Before(since) || e.Host == "" {
			continue
		}
		host, _, _ := strings.Cut(e.Host, ":")
		// Denials by Anthropic or Obot's own services aren't hosts an administrator would allow.
		if host == "api.anthropic.com" || strings.HasSuffix(host, ".svc") {
			continue
		}
		key := e.EnvironmentID + "|" + host
		g := groups[key]
		if g == nil {
			g = &blockedHost{EnvironmentID: e.EnvironmentID, Environment: names[e.EnvironmentID], Host: host, Example: e.Method + " " + e.Path}
			groups[key] = g
		}
		g.Count++
		if e.CreatedAt.After(g.LastSeen) {
			g.LastSeen = e.CreatedAt
		}
		if user := h.userLabel(ctx, req, e.UserID); user != "" && !slices.Contains(g.Users, user) {
			g.Users = append(g.Users, user)
		}
	}
	out := make([]blockedHost, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, nil
}

func (h *Handler) addAuditTools(s *mcp.Server, req api.Context) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "search_audit_logs",
		Description: "Search Obot's audit logs: MCP gateway calls, LLM gateway requests, or Claude environment " +
			"network requests. Returns the most recent matching records, without request or response bodies.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in auditSearchInput) (*mcp.CallToolResult, any, error) {
		hours := in.Hours
		if hours <= 0 {
			hours = 24
		}
		start := time.Now().UTC().Add(-time.Duration(min(hours, 2160)) * time.Hour)
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		limit = min(limit, 100)
		userID := ""
		if email := strings.TrimSpace(in.User); email != "" {
			users, err := req.GatewayClient.Users(ctx, gtypes.UserQuery{Email: email})
			if err != nil {
				return toolError(err)
			}
			if len(users) == 0 {
				return toolError(fmt.Errorf("no Obot user has the email %s", email))
			}
			userID = fmt.Sprint(users[0].ID)
		}
		q := url.Values{"limit": {strconv.Itoa(limit)}}
		var path string
		switch strings.ToLower(in.Log) {
		case "mcp":
			path = "/mcp-audit-logs"
			q.Set("start_time", start.Format(time.RFC3339))
			setIf(q, "query", in.Query)
			setIf(q, "user_id", userID)
			setIf(q, "outcome", in.Outcome)
		case "llm":
			path = "/llm-audit-logs"
			q.Set("start_time", start.Format(time.RFC3339))
			q.Set("hide_models_requests", "true")
			setIf(q, "query", in.Query)
			setIf(q, "user_id", userID)
			setIf(q, "outcome", in.Outcome)
		case "egress":
			path = "/egress-audit-logs"
			q.Set("exclude_category", "control-plane,runner")
			setIf(q, "host", in.Query)
			setIf(q, "user_id", userID)
			setIf(q, "outcome", in.Outcome)
		default:
			return toolError(fmt.Errorf("log must be mcp, llm or egress"))
		}
		var page map[string]any
		if err := h.callAPI(ctx, req, "GET", path, q, nil, &page); err != nil {
			return toolError(err)
		}
		items, _ := page["items"].([]any)
		records := make([]any, 0, len(items))
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				if in.Log == "egress" {
					if created, ok := m["createdAt"].(string); ok {
						if t, err := time.Parse(time.RFC3339Nano, created); err == nil && t.Before(start) {
							continue
						}
					}
				}
				records = append(records, trimRecord(m))
			}
		}
		return jsonResult(map[string]any{"log": in.Log, "records": records, "total": page["total"]})
	})
}

func setIf(q url.Values, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		q.Set(key, value)
	}
}

// trimRecord drops bodies and headers from an audit record and shortens long values, so a page
// of records fits in the model's context.
func trimRecord(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		lower := strings.ToLower(k)
		if strings.Contains(lower, "body") || strings.Contains(lower, "headers") || strings.Contains(lower, "content") {
			continue
		}
		if s, ok := v.(string); ok && len(s) > 300 {
			v = s[:300] + "…"
		}
		out[k] = v
	}
	return out
}
