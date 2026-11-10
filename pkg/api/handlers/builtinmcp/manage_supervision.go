package builtinmcp

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/provenance"
)

type policyListInput struct {
	Query string `json:"query,omitempty" jsonschema:"Words to look for in policy names, descriptions and conditions."`
}

type policyIDInput struct {
	ID string `json:"id" jsonschema:"The policy's ID, from list_policies."`
}

type draftPolicyInput struct {
	Text string `json:"text" jsonschema:"The rule in plain language, such as 'ask me before PII goes to an external host'."`
}

type savePolicyInput struct {
	ID          string   `json:"id,omitempty" jsonschema:"The policy to change. Empty creates a new policy."`
	Name        string   `json:"name,omitempty" jsonschema:"The policy's name. Required for a new policy."`
	Description string   `json:"description,omitempty" jsonschema:"What the policy is for."`
	Condition   string   `json:"condition,omitempty" jsonschema:"The CEL condition. Required for a new policy; draft one with draft_policy."`
	Effect      string   `json:"effect,omitempty" jsonschema:"allow, deny, require_approval or judge. Required for a new policy."`
	Mode        string   `json:"mode,omitempty" jsonschema:"shadow (record what it would do; new policies default to this) or enforce. Only enforce when the administrator asked to."`
	Enabled     *bool    `json:"enabled,omitempty" jsonschema:"Turn the policy on or off."`
	Priority    *int     `json:"priority,omitempty" jsonschema:"Higher runs first."`
	Surfaces    []string `json:"surfaces,omitempty" jsonschema:"Limit to these surfaces: llm, mcp, device, egress, copilot-studio. Empty means all."`
	Guidance    string   `json:"guidance,omitempty" jsonschema:"Guidance for the judge or for approvers."`
}

type approvalListInput struct {
	Status string `json:"status,omitempty" jsonschema:"pending (the default), approved, denied, expired or all."`
	Limit  int    `json:"limit,omitempty" jsonschema:"How many. Defaults to 20."`
}

type decideApprovalInput struct {
	ID      string `json:"id" jsonschema:"The approval's ID."`
	Approve bool   `json:"approve" jsonschema:"true approves the held action, false denies it."`
	Note    string `json:"note,omitempty" jsonschema:"A note recorded with the decision."`
}

type sessionIDInput struct {
	ID     string `json:"id" jsonschema:"The agent session's ID (ses-…)."`
	Events int    `json:"events,omitempty" jsonschema:"How many of its latest events to include. Defaults to 20, at most 100."`
}

type killInput struct {
	Scope   string `json:"scope" jsonschema:"session, user, agent or org. Prefer the narrowest scope that stops the problem."`
	Target  string `json:"target,omitempty" jsonschema:"The session ID, user ID or agent name. Empty for org."`
	Reason  string `json:"reason" jsonschema:"Why. People see this."`
	Minutes int    `json:"minutes,omitempty" jsonschema:"Lift the kill after this many minutes. Empty means until lifted."`
}

type toolLabelsInput struct {
	Server string   `json:"server" jsonschema:"The tool's scope: catalog-entry:<entry ID> or vmcp:<vMCP ID>."`
	Tool   string   `json:"tool" jsonschema:"The tool's name."`
	Labels []string `json:"labels" jsonschema:"The tool's labels, replacing the current ones: content labels its results carry (confidential, untrusted, secret, custom labels) and capabilities (writes-external, writes-internal, exec, destructive). Empty clears them."`
}

type customLabelInput struct {
	provenance.CustomLabel
	Create bool `json:"create,omitempty" jsonschema:"true creates a new label; false updates the one with this name."`
}

type nameInput struct {
	Name string `json:"name" jsonschema:"The custom label's name."`
}

type usersInput struct {
	Query string `json:"query,omitempty" jsonschema:"Text to match in email, username or name."`
	Role  string `json:"role,omitempty" jsonschema:"Only users with this role, such as admin, owner, power-user or basic."`
}

// addSupervisionTools gives the admin server the rest of what the UI can do: policies, approvals,
// sessions and kill switches, labels, users and model access.
func (h *Handler) addSupervisionTools(s *mcp.Server, req api.Context) {
	h.addPolicyTools(s, req)
	h.addSessionControlTools(s, req)
	h.addLabelTools(s, req)
	h.addIdentityTools(s, req)
}

func (h *Handler) addPolicyTools(s *mcp.Server, req api.Context) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_policies",
		Description: "List Obot Policy rules: CEL conditions over sessions and actions, with their effect, mode (shadow or enforce) and whether they're enabled.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in policyListInput) (*mcp.CallToolResult, any, error) {
		var page struct {
			Items []provenance.Policy `json:"items"`
		}
		if err := h.callAPI(ctx, req, "GET", "/provenance/policies", nil, nil, &page); err != nil {
			return toolError(err)
		}
		terms := strings.Fields(strings.ToLower(in.Query))
		out := []map[string]any{}
		for _, p := range page.Items {
			if !containsAll(strings.ToLower(p.Name+" "+p.Description+" "+p.Condition), terms) {
				continue
			}
			out = append(out, policyView(p, h.serverURL))
		}
		return jsonResult(map[string]any{"policies": out})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_policy",
		Description: "Get one policy and the actions it recently decided or, in shadow mode, would have.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in policyIDInput) (*mcp.CallToolResult, any, error) {
		var p provenance.Policy
		if err := h.callAPI(ctx, req, "GET", "/provenance/policies/"+url.PathEscape(in.ID), nil, nil, &p); err != nil {
			return toolError(err)
		}
		var events struct {
			Items []provenance.Event `json:"items"`
			Total int                `json:"total"`
		}
		_ = h.callAPI(ctx, req, "GET", "/provenance/events", url.Values{"policy_id": {in.ID}, "limit": {"10"}}, nil, &events)
		matches := make([]map[string]any, 0, len(events.Items))
		for _, ev := range events.Items {
			matches = append(matches, eventView(ev))
		}
		view := policyView(p, h.serverURL)
		view["recentMatches"] = matches
		view["totalMatches"] = events.Total
		return jsonResult(view)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "draft_policy",
		Description: "Have the Supervisor turn a rule in plain language into a policy draft (a CEL condition and effect), " +
			"tested against examples. Nothing is saved; show the draft and save it with save_policy if the administrator wants it.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in draftPolicyInput) (*mcp.CallToolResult, any, error) {
		var draft map[string]any
		if err := h.callAPI(ctx, req, "POST", "/provenance/policies/draft", nil, map[string]string{"text": in.Text}, &draft); err != nil {
			return toolError(err)
		}
		return jsonResult(draft)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "save_policy",
		Description: "Create a policy or change one: its condition, effect, mode, enabled state or priority. New policies " +
			"start in shadow mode. Only when the administrator asked for this change; switching to enforce or deny changes " +
			"what agents can do, so confirm it first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in savePolicyInput) (*mcp.CallToolResult, any, error) {
		var p provenance.Policy
		if in.ID != "" {
			if err := h.callAPI(ctx, req, "GET", "/provenance/policies/"+url.PathEscape(in.ID), nil, nil, &p); err != nil {
				return toolError(err)
			}
		} else {
			if in.Name == "" || in.Condition == "" || in.Effect == "" {
				return toolError(fmt.Errorf("a new policy needs a name, a condition and an effect"))
			}
			p.Enabled = true
			p.Mode = "shadow"
			p.Priority = 500
		}
		if in.Name != "" {
			p.Name = in.Name
		}
		if in.Description != "" {
			p.Description = in.Description
		}
		if in.Condition != "" {
			p.Condition = in.Condition
		}
		if in.Effect != "" {
			p.Effect = provenance.Effect(in.Effect)
		}
		if in.Mode != "" {
			p.Mode = in.Mode
		}
		if in.Enabled != nil {
			p.Enabled = *in.Enabled
		}
		if in.Priority != nil {
			p.Priority = *in.Priority
		}
		if in.Surfaces != nil {
			p.Surfaces = nil
			for _, surface := range in.Surfaces {
				p.Surfaces = append(p.Surfaces, provenance.Surface(surface))
			}
		}
		if in.Guidance != "" {
			p.Guidance = in.Guidance
		}
		var saved provenance.Policy
		var err error
		if in.ID != "" {
			err = h.callAPI(ctx, req, "PUT", "/provenance/policies/"+url.PathEscape(in.ID), nil, p, &saved)
		} else {
			err = h.callAPI(ctx, req, "POST", "/provenance/policies", nil, p, &saved)
		}
		if err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"policy": policyView(saved, h.serverURL), "message": "Saved."})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_policy",
		Description: "Delete a policy. Built-in policies can only be disabled. Only when the administrator clearly asked for this policy to be deleted.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in policyIDInput) (*mcp.CallToolResult, any, error) {
		if err := h.callAPI(ctx, req, "DELETE", "/provenance/policies/"+url.PathEscape(in.ID), nil, nil, nil); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"message": "Deleted policy " + in.ID + "."})
	})
}

func policyView(p provenance.Policy, serverURL string) map[string]any {
	return map[string]any{
		"id":          p.ID,
		"name":        p.Name,
		"description": p.Description,
		"condition":   p.Condition,
		"effect":      p.Effect,
		"mode":        p.Mode,
		"enabled":     p.Enabled,
		"priority":    p.Priority,
		"surfaces":    p.Surfaces,
		"builtIn":     p.BuiltIn,
		"url":         serverURL + "/policies/" + p.ID,
	}
}

func eventView(ev provenance.Event) map[string]any {
	v := map[string]any{
		"id":        ev.ID,
		"sessionID": ev.SessionID,
		"at":        ev.At,
		"surface":   ev.Surface,
		"kind":      ev.Kind,
		"summary":   ev.Summary,
		"labels":    ev.Labels,
	}
	if d := ev.Decision; d != nil {
		v["decision"] = map[string]any{"allow": d.Allow, "effect": d.Effect, "shadow": d.Shadow, "reason": d.Reason, "approvalID": d.ApprovalID}
	}
	return v
}

func (h *Handler) addSessionControlTools(s *mcp.Server, req api.Context) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_agent_session",
		Description: "Get one agent session: its risk, labels and where they came from, counters, and its latest events with their policy decisions.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIDInput) (*mcp.CallToolResult, any, error) {
		var session provenance.Session
		if err := h.callAPI(ctx, req, "GET", "/provenance/sessions/"+url.PathEscape(in.ID), nil, nil, &session); err != nil {
			return toolError(err)
		}
		limit := in.Events
		if limit <= 0 {
			limit = 20
		}
		var events struct {
			Items []provenance.Event `json:"items"`
		}
		_ = h.callAPI(ctx, req, "GET", "/provenance/sessions/"+url.PathEscape(in.ID)+"/events", url.Values{"limit": {strconv.Itoa(min(limit, 100))}}, nil, &events)
		list := make([]map[string]any, 0, len(events.Items))
		for _, ev := range events.Items {
			list = append(list, eventView(ev))
		}
		session.State.Fingerprints = nil
		return jsonResult(map[string]any{
			"session": map[string]any{
				"id": session.ID, "title": session.Title, "user": cmpOr(session.Email, session.UserID), "agent": session.Agent,
				"startedAt": session.StartedAt, "lastActivityAt": session.LastActivityAt, "open": session.EndedAt == nil,
				"risk": session.State.Risk, "labels": session.State.Labels, "labelOrigins": session.State.Origins,
				"events": session.State.Events, "toolCalls": session.State.ToolCalls, "denied": session.State.Denied,
				"approvals": session.State.Approvals, "killed": session.State.Killed, "url": h.serverURL + "/sessions/" + session.ID,
			},
			"latestEvents": list,
			"display":      "In Obot AI's chat, a session widget shows this session live.",
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_approvals",
		Description: "List agent actions policies held for a person's approval, pending ones by default, with the Supervisor's recommendation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in approvalListInput) (*mcp.CallToolResult, any, error) {
		q := url.Values{"limit": {strconv.Itoa(cmpInt(in.Limit, 20))}}
		switch status := strings.ToLower(in.Status); status {
		case "", "pending":
			q.Set("status", "pending")
		case "all":
		default:
			q.Set("status", status)
		}
		var page map[string]any
		if err := h.callAPI(ctx, req, "GET", "/provenance/approvals", q, nil, &page); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"approvals": page["items"], "total": page["total"],
			"display": "In Obot AI's chat, the approvals widget lets the administrator decide these with buttons."})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "decide_approval",
		Description: "Approve or deny an action a policy held. Only when the administrator clearly asked to decide this " +
			"approval in words; otherwise show the approvals widget so they can click.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in decideApprovalInput) (*mcp.CallToolResult, any, error) {
		verb := "deny"
		if in.Approve {
			verb = "approve"
		}
		var out map[string]any
		if err := h.callAPI(ctx, req, "POST", "/provenance/approvals/"+url.PathEscape(in.ID)+"/"+verb, nil, map[string]string{"note": in.Note}, &out); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"approval": out, "message": "Done: " + verb + "d."})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_kills",
		Description: "List active kill switches: sessions, users, agents or the whole organization whose actions are all denied.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		var page map[string]any
		if err := h.callAPI(ctx, req, "GET", "/provenance/kills", nil, nil, &page); err != nil {
			return toolError(err)
		}
		return jsonResult(page)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "kill",
		Description: "Activate a kill switch: deny every action of a session, user, agent or the whole organization on every " +
			"surface. Only when the administrator clearly asked for it; confirm the scope and target first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in killInput) (*mcp.CallToolResult, any, error) {
		var out map[string]any
		body := map[string]any{"scope": in.Scope, "target": in.Target, "reason": in.Reason, "ttlSeconds": in.Minutes * 60}
		if err := h.callAPI(ctx, req, "POST", "/provenance/kills", nil, body, &out); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"kill": out, "message": "Kill switch on. Lift it with lift_kill."})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "lift_kill",
		Description: "Lift a kill switch, by its ID from list_kills. Only when the administrator asked.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, any, error) {
		if err := h.callAPI(ctx, req, "DELETE", "/provenance/kills/"+url.PathEscape(in.ID), nil, nil, nil); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"message": "Lifted."})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "end_agent_session",
		Description: "Mark an agent session as ended. Its next action starts a new session. Only when the administrator asked.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, any, error) {
		if err := h.callAPI(ctx, req, "POST", "/provenance/sessions/"+url.PathEscape(in.ID)+"/end", nil, nil, nil); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"message": "Ended."})
	})
}

func (h *Handler) addLabelTools(s *mcp.Server, req api.Context) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_labels",
		Description: "The label vocabulary: built-in content labels and capabilities, the organization's custom labels and " +
			"their detectors, and how many tools have reviewed labels. Policies are written in terms of these labels.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		var inventory map[string]any
		if err := h.callAPI(ctx, req, "GET", "/provenance/label-inventory", nil, nil, &inventory); err != nil {
			return toolError(err)
		}
		out := map[string]any{}
		for _, k := range []string{"capabilities", "contentLabels", "customLabels", "contentTagging", "supervisorEnabled"} {
			if v, ok := inventory[k]; ok {
				out[k] = v
			}
		}
		if resources, ok := inventory["resources"].([]any); ok {
			out["resources"] = len(resources)
		}
		out["url"] = h.serverURL + "/tool-labels"
		return jsonResult(out)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "set_tool_labels",
		Description: "Set the labels an MCP tool carries, as an administrator's reviewed decision: what its results are " +
			"(confidential, untrusted, secret, or custom labels) and what it can do (writes-external, writes-internal, exec, " +
			"destructive). Policies act on these. Confirm with the administrator first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in toolLabelsInput) (*mcp.CallToolResult, any, error) {
		var out map[string]any
		body := map[string]any{"server": in.Server, "tool": in.Tool, "labels": in.Labels}
		if err := h.callAPI(ctx, req, "PUT", "/provenance/tool-labels", nil, body, &out); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"toolLabel": out, "message": "Saved."})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "save_custom_label",
		Description: "Create or update a custom label: a name, a plain-language description, and how it's applied: ai " +
			"(the content tagger checks the description), patterns ({type: keyword|regex, value}) and sources ({type: " +
			"catalog-entry|vmcp|host, id, tool?}). confidential also adds the confidential label. Confirm with the administrator first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in customLabelInput) (*mcp.CallToolResult, any, error) {
		var out map[string]any
		var err error
		if in.Create {
			err = h.callAPI(ctx, req, "POST", "/provenance/custom-labels", nil, in.CustomLabel, &out)
		} else {
			err = h.callAPI(ctx, req, "PUT", "/provenance/custom-labels/"+url.PathEscape(string(in.Name)), nil, in.CustomLabel, &out)
		}
		if err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"label": out, "message": "Saved."})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_custom_label",
		Description: "Delete a custom label. Policies that test it stop matching. Only when the administrator clearly asked.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in nameInput) (*mcp.CallToolResult, any, error) {
		if err := h.callAPI(ctx, req, "DELETE", "/provenance/custom-labels/"+url.PathEscape(in.Name), nil, nil, nil); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"message": "Deleted " + in.Name + "."})
	})
}

func (h *Handler) addIdentityTools(s *mcp.Server, req api.Context) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_users",
		Description: "List Obot users with their role, email and when they were last active.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in usersInput) (*mcp.CallToolResult, any, error) {
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := h.callAPI(ctx, req, "GET", "/users", nil, nil, &page); err != nil {
			return toolError(err)
		}
		q := strings.ToLower(strings.TrimSpace(in.Query))
		out := []map[string]any{}
		for _, u := range page.Items {
			text := strings.ToLower(fmt.Sprint(u["email"], " ", u["username"], " ", u["displayName"]))
			if q != "" && !strings.Contains(text, q) {
				continue
			}
			if in.Role != "" && !strings.Contains(strings.ToLower(fmt.Sprint(u["role"], " ", u["effectiveRole"], " ", u["groups"])), strings.ToLower(in.Role)) {
				continue
			}
			view := map[string]any{}
			for _, k := range []string{"id", "email", "username", "displayName", "role", "effectiveRole", "groups", "lastActiveDay", "created"} {
				if v, ok := u[k]; ok && v != nil && v != "" {
					view[k] = v
				}
			}
			out = append(out, view)
			if len(out) == 100 {
				break
			}
		}
		return jsonResult(map[string]any{"users": out, "total": len(page.Items)})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_model_access",
		Description: "The LLM gateway's models and model access policies: which models exist, which aliases point where, and who may use which model.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		var models struct {
			Items []map[string]any `json:"items"`
		}
		if err := h.callAPI(ctx, req, "GET", "/models", nil, nil, &models); err != nil {
			return toolError(err)
		}
		var policies struct {
			Items []map[string]any `json:"items"`
		}
		if err := h.callAPI(ctx, req, "GET", "/model-access-policies", nil, nil, &policies); err != nil {
			return toolError(err)
		}
		names := map[string]string{}
		modelList := make([]map[string]any, 0, len(models.Items))
		for _, m := range models.Items {
			names[fmt.Sprint(m["id"])] = fmt.Sprint(m["name"])
			modelList = append(modelList, map[string]any{"id": m["id"], "name": m["name"], "target": m["targetModel"], "alias": m["aliasAssigned"], "active": m["active"]})
		}
		policyList := make([]map[string]any, 0, len(policies.Items))
		for _, p := range policies.Items {
			var granted []string
			if ms, ok := p["models"].([]any); ok {
				for _, m := range ms {
					if mm, ok := m.(map[string]any); ok {
						granted = append(granted, cmpOr(names[fmt.Sprint(mm["id"])], fmt.Sprint(mm["id"])))
					}
				}
			}
			policyList = append(policyList, map[string]any{"id": p["id"], "name": p["displayName"], "subjects": p["subjects"], "models": granted})
		}
		return jsonResult(map[string]any{"models": modelList, "accessPolicies": policyList})
	})
}
