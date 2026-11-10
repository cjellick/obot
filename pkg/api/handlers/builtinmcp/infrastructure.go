package builtinmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/api/handlers"
	"github.com/obot-platform/obot/pkg/obotlogs"
)

// infraResultBudget keeps a tool's result under the assistant's toolResultLimit, with room
// for the JSON around the lines.
const infraResultBudget = 10500

type infraStatusInput struct {
	Area           string `json:"area,omitempty" jsonschema:"Only this area: mcp (MCP server workloads), claude (Claude environment workers) or obot (Obot's own pods). All by default."`
	IncludeHealthy bool   `json:"includeHealthy,omitempty" jsonschema:"List healthy workloads in full too. By default they're only counted and named."`
}

type infraWorkloadInput struct {
	ID string `json:"id" jsonschema:"The MCP server's ID (ms1...), a vMCP ID, or words from its name. The deployment is named after the MCP server's ID."`
}

type infraLogsInput struct {
	ID       string `json:"id" jsonschema:"The MCP server's ID (ms1...), a vMCP ID, or words from its name."`
	Query    string `json:"query,omitempty" jsonschema:"Only lines containing all these words (case-insensitive), e.g. error or a tool name."`
	Tail     int    `json:"tail,omitempty" jsonschema:"How many of the newest lines to read before filtering (default 150, at most 2000)."`
	Previous bool   `json:"previous,omitempty" jsonschema:"Read the previous container's logs: what it printed before its last crash or restart."`
	Pod      string `json:"pod,omitempty" jsonschema:"A specific pod. The newest pod by default."`
}

type obotLogsInput struct {
	Query string `json:"query,omitempty" jsonschema:"Only lines containing all these words (case-insensitive): an error text, a server or user ID, a component such as bifrost or mcp."`
	Level string `json:"level,omitempty" jsonschema:"Minimum level: debug, info, warn or error. Use warn or error to find what went wrong."`
	Since string `json:"since,omitempty" jsonschema:"Only lines newer than this: a duration such as 15m or 2h, or an RFC 3339 time."`
	Limit int    `json:"limit,omitempty" jsonschema:"How many of the newest matching lines to return (default 80)."`
}

func (h *Handler) addInfrastructureTools(s *mcp.Server, req api.Context) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "infrastructure_status",
		Description: "Health of the Kubernetes workloads Obot runs: the MCP servers it hosts, the Claude environment worker " +
			"pods, and Obot's own pods. Problems come first (crash loops, image pull failures, unschedulable pods, OOM " +
			"kills, restarts, warning events). Start here for \"why is X down?\", \"is anything broken?\" or \"why can't " +
			"users connect to a server?\", then use get_mcp_server_workload and read_mcp_server_logs on what looks wrong.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in infraStatusInput) (*mcp.CallToolResult, any, error) {
		var infra handlers.Infrastructure
		if err := h.callAPI(ctx, req, "GET", "/infrastructure", nil, nil, &infra); err != nil {
			return toolError(err)
		}
		return compactResult(h.infraStatusView(infra, strings.ToLower(strings.TrimSpace(in.Area)), in.IncludeHealthy))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_mcp_server_workload",
		Description: "One MCP server's Kubernetes deployment: its pods (status, restarts, last termination reason such " +
			"as OOMKilled), image, resource requests and limits, and every recent event, warnings first. Use it to find " +
			"why a server won't start or keeps restarting.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in infraWorkloadInput) (*mcp.CallToolResult, any, error) {
		name, candidates, err := h.resolveWorkload(ctx, req, in.ID)
		if err != nil {
			return toolError(err)
		}
		if name == "" {
			return compactResult(map[string]any{"message": "Several MCP server workloads match; call again with one ID.", "candidates": candidates})
		}
		var w handlers.InfraWorkload
		if err := h.callAPI(ctx, req, "GET", "/infrastructure/mcp-servers/"+url.PathEscape(name), nil, nil, &w); err != nil {
			return toolError(err)
		}
		if len(w.Events) > 25 {
			w.Events = w.Events[:25]
		}
		return compactResult(h.workloadView(w, true))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "read_mcp_server_logs",
		Description: "Read an MCP server's own log output from its Kubernetes pod. Use it after get_mcp_server_workload " +
			"to see why a server fails: startup errors, missing configuration, crashes (set previous to read what the " +
			"container printed before it last restarted) or failing tool calls.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in infraLogsInput) (*mcp.CallToolResult, any, error) {
		name, candidates, err := h.resolveWorkload(ctx, req, in.ID)
		if err != nil {
			return toolError(err)
		}
		if name == "" {
			return compactResult(map[string]any{"message": "Several MCP server workloads match; call again with one ID.", "candidates": candidates})
		}
		q := url.Values{}
		q.Set("tail", strconv.Itoa(cmpInt(in.Tail, 150)))
		if in.Query != "" {
			q.Set("query", in.Query)
		}
		if in.Previous {
			q.Set("previous", "true")
		}
		if in.Pod != "" {
			q.Set("pod", in.Pod)
		}
		var out struct {
			Deployment string   `json:"deployment"`
			Pod        string   `json:"pod"`
			Container  string   `json:"container"`
			Previous   bool     `json:"previous"`
			Lines      []string `json:"lines"`
		}
		if err := h.callAPI(ctx, req, "GET", "/infrastructure/mcp-servers/"+url.PathEscape(name)+"/logs", q, nil, &out); err != nil {
			return toolError(err)
		}
		lines, dropped := fitLines(out.Lines, 1000, infraResultBudget)
		result := map[string]any{"mcpServerID": out.Deployment, "pod": out.Pod, "container": out.Container, "previous": out.Previous, "lines": lines}
		if dropped > 0 {
			result["omittedOlderLines"] = dropped
		}
		if len(lines) == 0 {
			result["message"] = "No log lines matched."
		}
		return compactResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "read_obot_logs",
		Description: "Search Obot's own recent server logs (this replica, since it started): errors and warnings from " +
			"the API, controllers, MCP gateway, LLM gateway (bifrost), authentication and deployments. Use it to debug " +
			"Obot itself, e.g. \"why did deploying Y fail?\", \"why is the LLM gateway returning errors?\" or \"why " +
			"can't a user sign in?\". Filter with level warn or error and words from the problem.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in obotLogsInput) (*mcp.CallToolResult, any, error) {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(cmpInt(in.Limit, 80)))
		for k, v := range map[string]string{"query": in.Query, "level": in.Level, "since": in.Since} {
			if v = strings.TrimSpace(v); v != "" {
				q.Set(k, v)
			}
		}
		var out struct {
			Pod      string          `json:"pod"`
			Lines    []obotlogs.Line `json:"lines"`
			Matched  int             `json:"matched"`
			Buffered int             `json:"buffered"`
			OldestAt time.Time       `json:"oldestAt"`
		}
		if err := h.callAPI(ctx, req, "GET", "/infrastructure/logs", q, nil, &out); err != nil {
			return toolError(err)
		}
		text := make([]string, 0, len(out.Lines))
		for _, l := range out.Lines {
			text = append(text, l.Time.UTC().Format(time.RFC3339)+" "+strings.ToUpper(cmpOr(l.Level, "-"))+" "+l.Text)
		}
		lines, dropped := fitLines(text, 700, infraResultBudget)
		result := map[string]any{
			"pod":            out.Pod,
			"matched":        out.Matched,
			"returned":       len(lines),
			"bufferedLines":  out.Buffered,
			"oldestBuffered": out.OldestAt,
			"lines":          lines,
		}
		if dropped > 0 {
			result["omittedOlderLines"] = dropped
		}
		if len(lines) == 0 {
			result["message"] = "No log lines matched. Older output may have rotated out of the buffer, or another replica handled the request."
		}
		return compactResult(result)
	})
}

// resolveWorkload finds the deployment for an MCP server ID, a vMCP ID or words from a name.
// With several matches it returns them as candidates and no name.
func (h *Handler) resolveWorkload(ctx context.Context, req api.Context, id string) (string, []map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil, fmt.Errorf("give an MCP server ID or name")
	}
	var infra handlers.Infrastructure
	if err := h.callAPI(ctx, req, "GET", "/infrastructure", nil, nil, &infra); err != nil {
		return "", nil, err
	}
	if !infra.Kubernetes {
		return "", nil, fmt.Errorf("%s", cmpOr(infra.Message, "Obot doesn't run MCP servers on Kubernetes"))
	}
	var matches []handlers.InfraWorkload
	terms := strings.Fields(strings.ToLower(id))
	for _, w := range infra.MCPServers.Workloads {
		if w.Name == id || w.MCPServerID == id {
			return w.Name, nil, nil
		}
		haystack := strings.ToLower(strings.Join([]string{w.Name, w.DisplayName, w.CatalogEntryID, w.CatalogEntryName, w.VMCPID, w.VMCPName, w.UserName}, " "))
		all := true
		for _, t := range terms {
			if !strings.Contains(haystack, t) {
				all = false
				break
			}
		}
		if all {
			matches = append(matches, w)
		}
	}
	switch len(matches) {
	case 0:
		return "", nil, fmt.Errorf("no MCP server workload matches %q. It may be stopped, remote (not run by Obot) or not deployed yet; infrastructure_status lists what's running", id)
	case 1:
		return matches[0].Name, nil, nil
	}
	candidates := make([]map[string]any, 0, len(matches))
	for _, w := range matches {
		candidates = append(candidates, map[string]any{"id": w.Name, "name": w.DisplayName, "vmcp": w.VMCPName, "user": w.UserName, "health": w.Health})
	}
	return "", candidates, nil
}

func (h *Handler) infraStatusView(infra handlers.Infrastructure, area string, includeHealthy bool) map[string]any {
	out := map[string]any{"engine": infra.Engine}
	if !infra.Kubernetes {
		out["message"] = infra.Message
		return out
	}
	problems := make([]map[string]any, 0, len(infra.Problems))
	for _, p := range infra.Problems {
		if area != "" && p.Area != area {
			continue
		}
		problem := map[string]any{"severity": p.Severity, "area": p.Area, "name": p.Name, "message": truncate(p.Message, 400)}
		if p.Workload != "" {
			problem["id"] = p.Workload
		}
		if p.Link != "" {
			problem["url"] = h.serverURL + p.Link
		}
		problems = append(problems, problem)
	}
	if len(problems) > 30 {
		problems = problems[:30]
	}
	out["problems"] = problems

	sections := map[string]handlers.InfraSection{"obot": infra.Obot, "mcp": infra.MCPServers, "claude": infra.Claude}
	for key, section := range sections {
		if area != "" && key != area {
			continue
		}
		if !section.Enabled {
			out[key] = map[string]any{"enabled": false}
			continue
		}
		counts := map[string]int{}
		var detailed []map[string]any
		var healthy []string
		for _, w := range section.Workloads {
			counts[w.Health]++
			if w.Health == "healthy" && !includeHealthy {
				healthy = append(healthy, w.DisplayName+" ("+w.Name+")")
				continue
			}
			detailed = append(detailed, h.workloadView(w, false))
		}
		view := map[string]any{"namespace": section.Namespace, "counts": counts}
		if section.Error != "" {
			view["error"] = section.Error
		}
		if len(detailed) > 0 {
			if len(detailed) > 25 {
				view["omittedWorkloads"] = len(detailed) - 25
				detailed = detailed[:25]
			}
			view["workloads"] = detailed
		}
		if len(healthy) > 0 {
			if len(healthy) > 40 {
				view["moreHealthy"] = len(healthy) - 40
				healthy = healthy[:40]
			}
			view["healthy"] = healthy
		}
		out[key] = view
	}
	return out
}

// workloadView is a compact workload for the model. full includes every pod and event.
func (h *Handler) workloadView(w handlers.InfraWorkload, full bool) map[string]any {
	v := map[string]any{
		"id":       w.Name,
		"name":     w.DisplayName,
		"health":   w.Health,
		"ready":    fmt.Sprintf("%d/%d", w.ReadyReplicas, w.Replicas),
		"restarts": w.Restarts,
		"age":      age(w.Created),
	}
	set := func(key, value string) {
		if value != "" {
			v[key] = value
		}
	}
	set("reason", truncate(w.HealthReason, 400))
	set("catalogEntry", w.CatalogEntryName)
	set("vmcp", w.VMCPName)
	set("vmcpID", w.VMCPID)
	set("user", w.UserName)
	set("environment", w.EnvironmentName)
	if w.Link != "" {
		v["url"] = h.serverURL + w.Link
	}
	if full {
		set("namespace", w.Namespace)
		set("image", w.Image)
		v["resources"] = w.InfraResources
	}
	pods := make([]map[string]any, 0, len(w.Pods))
	for _, p := range w.Pods {
		pod := map[string]any{"name": p.Name, "status": p.Status, "ready": p.Ready, "restarts": p.Restarts, "age": age(p.Created)}
		if p.LastTermination != "" {
			pod["lastTermination"] = p.LastTermination
		}
		if p.Message != "" {
			pod["message"] = truncate(p.Message, 300)
		}
		if len(p.Sessions) > 0 {
			sessions := make([]string, 0, len(p.Sessions))
			for _, s := range p.Sessions {
				sessions = append(sessions, fmt.Sprintf("%s by %s (%s)", cmpOr(s.Name, s.ID), cmpOr(s.UserName, s.UserID), s.State))
			}
			pod["sessions"] = sessions
		}
		pods = append(pods, pod)
		if !full && len(pods) == 3 {
			break
		}
	}
	v["pods"] = pods
	limit := 3
	if full {
		limit = len(w.Events)
	}
	events := make([]string, 0, min(limit, len(w.Events)))
	for _, e := range w.Events {
		if len(events) == limit {
			break
		}
		if !full && e.Type != "Warning" {
			continue
		}
		count := ""
		if e.Count > 1 {
			count = fmt.Sprintf(" (x%d)", e.Count)
		}
		events = append(events, fmt.Sprintf("%s %s %s %s: %s%s", age(e.LastSeen)+" ago", e.Type, e.Object, e.Reason, truncate(e.Message, 300), count))
	}
	if len(events) > 0 {
		v["events"] = events
	}
	return v
}

// fitLines cuts each line to lineLimit and keeps the newest lines that fit in budget bytes.
// It returns the lines, oldest first, and how many older lines it dropped.
func fitLines(lines []string, lineLimit, budget int) ([]string, int) {
	used := 0
	start := len(lines)
	for start > 0 {
		l := truncate(lines[start-1], lineLimit)
		if used+len(l)+4 > budget {
			break
		}
		used += len(l) + 4
		start--
	}
	out := make([]string, 0, len(lines)-start)
	for _, l := range lines[start:] {
		out = append(out, truncate(l, lineLimit))
	}
	return out, start
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func age(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func cmpInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// compactResult is jsonResult without indentation, so more fits in the model's budget.
func compactResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}
