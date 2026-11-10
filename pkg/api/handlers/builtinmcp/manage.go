package builtinmcp

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	gtypes "github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/system"
)

const defaultCatalog = system.DefaultCatalog

type idInput struct {
	ID string `json:"id" jsonschema:"The ID."`
}

type queryInput struct {
	Query string `json:"query,omitempty" jsonschema:"Words to look for in names and descriptions. Empty lists everything."`
}

type accessInput struct {
	Everyone bool     `json:"everyone,omitempty" jsonschema:"Give every Obot user access."`
	Admins   bool     `json:"admins,omitempty" jsonschema:"Give Obot administrators access."`
	Users    []string `json:"users,omitempty" jsonschema:"Give these users access, by email."`
	Groups   []string `json:"groups,omitempty" jsonschema:"Give these identity provider groups access, by group ID."`
}

type createVMCPInput struct {
	Name            string   `json:"name" jsonschema:"The vMCP's display name."`
	Description     string   `json:"description,omitempty" jsonschema:"What the vMCP is for."`
	CatalogEntryIDs []string `json:"catalogEntryIDs,omitempty" jsonschema:"Catalog entries (MCP servers) to put in it. Find them with list_catalog_entries."`
	accessInput
}

type updateVMCPInput struct {
	ID                 string   `json:"id" jsonschema:"The vMCP's ID."`
	Name               string   `json:"name,omitempty" jsonschema:"A new display name."`
	Description        string   `json:"description,omitempty" jsonschema:"A new description."`
	AddCatalogEntryIDs []string `json:"addCatalogEntryIDs,omitempty" jsonschema:"Catalog entries to add as components."`
	RemoveComponents   []string `json:"removeComponents,omitempty" jsonschema:"Components to remove, by component ID or name (from get_vmcp)."`
}

type grantVMCPInput struct {
	ID      string `json:"id" jsonschema:"The vMCP's ID."`
	Profile string `json:"profile,omitempty" jsonschema:"The profile to add the people to. A new profile with this name is created when there's none; it defaults to a name made from who gets access."`
	accessInput
	Components []string `json:"components,omitempty" jsonschema:"Limit a new profile to these components, by ID or name. Empty grants every component."`
}

type createEntryInput struct {
	types.MCPServerRequestManifest
}

type updateEntryInput struct {
	ID               string `json:"id" jsonschema:"The catalog entry's ID."`
	Name             string `json:"name,omitempty" jsonschema:"A new display name."`
	ShortDescription string `json:"shortDescription,omitempty" jsonschema:"A new one-line description."`
	Description      string `json:"description,omitempty" jsonschema:"A new description."`
	RemoteURL        string `json:"remoteURL,omitempty" jsonschema:"A new MCP endpoint, for a remote server."`
}

type egressInput struct {
	EnvironmentID string   `json:"environmentID" jsonschema:"The Claude environment's ID (cen-…), from list_claude_environments."`
	Profile       string   `json:"profile,omitempty" jsonschema:"The profile to change, by name. Empty changes the environment-wide list every profile gets."`
	AllowHosts    []string `json:"allowHosts,omitempty" jsonschema:"Hosts to allow, such as pypi.org or *.pythonhosted.org."`
	RemoveHosts   []string `json:"removeHosts,omitempty" jsonschema:"Hosts to stop allowing."`
}

type profileSettingsInput struct {
	EnvironmentID  string `json:"environmentID" jsonschema:"The Claude environment's ID (cen-…)."`
	Profile        string `json:"profile" jsonschema:"The profile to change, by name."`
	AllowClaudeMCP *bool  `json:"allowClaudeMCP,omitempty" jsonschema:"Let sessions use the MCP servers connected to the user's Claude account, through Claude's relay."`
	AllowAllEgress *bool  `json:"allowAllEgress,omitempty" jsonschema:"Let sessions reach any host. Prefer allowing specific hosts."`
	accessInput
}

type blockedEgressInput struct {
	Days          int    `json:"days,omitempty" jsonschema:"How many days back to look. Defaults to 7."`
	EnvironmentID string `json:"environmentID,omitempty" jsonschema:"Only this environment."`
}

type auditSearchInput struct {
	Log     string `json:"log" jsonschema:"Which audit log: mcp (MCP gateway calls), llm (LLM gateway requests, including Obot's own model calls under /obot/…) or egress (Claude environment network requests)."`
	Query   string `json:"query,omitempty" jsonschema:"Text to search for. For egress, a host."`
	User    string `json:"user,omitempty" jsonschema:"Only this user's records, by email."`
	Outcome string `json:"outcome,omitempty" jsonschema:"Only records with this outcome. mcp: success, failure, denied, timeout. llm: success, error, denied. egress: complete, denied, interrupted."`
	Hours   int    `json:"hours,omitempty" jsonschema:"How many hours back to look. Defaults to 24, at most 2160 (90 days)."`
	Limit   int    `json:"limit,omitempty" jsonschema:"How many records. Defaults to 20, at most 100."`
}

// addManagementTools gives the admin server tools to manage vMCPs, the catalog and Claude
// environments, and to search the audit logs. Each one calls Obot's API as the user.
func (h *Handler) addManagementTools(s *mcp.Server, req api.Context) {
	h.addVMCPTools(s, req)
	h.addCatalogTools(s, req)
	h.addClaudeEnvironmentTools(s, req)
	h.addAuditTools(s, req)
}

func (h *Handler) addVMCPTools(s *mcp.Server, req api.Context) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_vmcp",
		Description: "Get a vMCP: its components (the MCP servers in it) and its profiles (who can use which components).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, any, error) {
		var vmcp types.VMCP
		if err := h.callAPI(ctx, req, "GET", "/vmcps/"+url.PathEscape(in.ID), nil, nil, &vmcp); err != nil {
			return toolError(err)
		}
		return jsonResult(h.vmcpView(ctx, req, vmcp))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "create_vmcp",
		Description: "Create a shared vMCP from catalog entries and say who can use it. Confirm the name, servers " +
			"and who gets access with the administrator first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createVMCPInput) (*mcp.CallToolResult, any, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return toolError(fmt.Errorf("a name is required"))
		}
		manifest := types.VMCPManifest{DisplayName: name, Description: in.Description}
		for _, id := range in.CatalogEntryIDs {
			component, err := h.component(ctx, req, id)
			if err != nil {
				return toolError(err)
			}
			manifest.Components = append(manifest.Components, component)
		}
		subjects, err := h.subjects(ctx, req, in.accessInput)
		if err != nil {
			return toolError(err)
		}
		if len(subjects) > 0 {
			manifest.Profiles = []types.VMCPProfile{{Name: profileName(in.accessInput), Subjects: subjects, Permissions: types.VMCPProfilePermissions{AllowAllComponents: true}}}
		}
		var vmcp types.VMCP
		if err := h.callAPI(ctx, req, "POST", "/vmcps", nil, manifest, &vmcp); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"vmcp": h.vmcpView(ctx, req, vmcp), "message": "Created."})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_vmcp",
		Description: "Rename a vMCP, change its description, or add and remove its MCP servers. Confirm the change with the administrator first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateVMCPInput) (*mcp.CallToolResult, any, error) {
		return h.updateVMCP(ctx, req, in.ID, func(m *types.VMCPManifest) error {
			if name := strings.TrimSpace(in.Name); name != "" {
				m.DisplayName = name
			}
			if in.Description != "" {
				m.Description = in.Description
			}
			for _, remove := range in.RemoveComponents {
				i := slices.IndexFunc(m.Components, func(c types.VMCPComponent) bool {
					return c.ID == remove || strings.EqualFold(c.Name, remove)
				})
				if i < 0 {
					return fmt.Errorf("the vMCP has no component %q", remove)
				}
				m.Components = slices.Delete(m.Components, i, i+1)
			}
			for _, id := range in.AddCatalogEntryIDs {
				component, err := h.component(ctx, req, id)
				if err != nil {
					return err
				}
				m.Components = append(m.Components, component)
			}
			return nil
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "grant_vmcp_access",
		Description: "Give people access to a vMCP by adding them to one of its profiles, or to a new profile. " +
			"Confirm who gets access with the administrator first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in grantVMCPInput) (*mcp.CallToolResult, any, error) {
		subjects, err := h.subjects(ctx, req, in.accessInput)
		if err != nil {
			return toolError(err)
		}
		if len(subjects) == 0 {
			return toolError(fmt.Errorf("say who gets access: everyone, admins, users or groups"))
		}
		return h.updateVMCP(ctx, req, in.ID, func(m *types.VMCPManifest) error {
			name := cmpOr(strings.TrimSpace(in.Profile), profileName(in.accessInput))
			i := slices.IndexFunc(m.Profiles, func(p types.VMCPProfile) bool { return strings.EqualFold(p.Name, name) })
			if i < 0 {
				profile := types.VMCPProfile{Name: name, Permissions: types.VMCPProfilePermissions{AllowAllComponents: len(in.Components) == 0}}
				if len(in.Components) > 0 {
					profile.Permissions.AllowedComponents = map[string]types.VMCPComponentSet{}
					for _, want := range in.Components {
						j := slices.IndexFunc(m.Components, func(c types.VMCPComponent) bool {
							return c.ID == want || strings.EqualFold(c.Name, want)
						})
						if j < 0 {
							return fmt.Errorf("the vMCP has no component %q", want)
						}
						profile.Permissions.AllowedComponents[m.Components[j].ID] = types.VMCPComponentSet{}
					}
				}
				m.Profiles = append(m.Profiles, profile)
				i = len(m.Profiles) - 1
			}
			for _, subject := range subjects {
				if !slices.Contains(m.Profiles[i].Subjects, subject) {
					m.Profiles[i].Subjects = append(m.Profiles[i].Subjects, subject)
				}
			}
			return nil
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_vmcp",
		Description: "Delete a vMCP. Everyone connected to it loses it. Only when the administrator has clearly asked for this vMCP to be deleted.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, any, error) {
		if err := h.callAPI(ctx, req, "DELETE", "/vmcps/"+url.PathEscape(in.ID), nil, nil, nil); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"message": "Deleted vMCP " + in.ID + "."})
	})
}

// updateVMCP reads a vMCP, applies change to its manifest and saves it.
func (h *Handler) updateVMCP(ctx context.Context, req api.Context, id string, change func(*types.VMCPManifest) error) (*mcp.CallToolResult, any, error) {
	var vmcp types.VMCP
	if err := h.callAPI(ctx, req, "GET", "/vmcps/"+url.PathEscape(id), nil, nil, &vmcp); err != nil {
		return toolError(err)
	}
	if err := change(&vmcp.VMCPManifest); err != nil {
		return toolError(err)
	}
	var updated types.VMCP
	if err := h.callAPI(ctx, req, "PUT", "/vmcps/"+url.PathEscape(id), nil, vmcp.VMCPManifest, &updated); err != nil {
		return toolError(err)
	}
	return jsonResult(map[string]any{"vmcp": h.vmcpView(ctx, req, updated), "message": "Saved."})
}

// component is a vMCP component for a catalog entry. The API fills in the snapshot.
func (h *Handler) component(ctx context.Context, req api.Context, entryID string) (types.VMCPComponent, error) {
	var entry types.MCPServerCatalogEntry
	if err := h.callAPI(ctx, req, "GET", "/mcp-catalogs/"+defaultCatalog+"/entries/"+url.PathEscape(entryID), nil, nil, &entry); err != nil {
		return types.VMCPComponent{}, fmt.Errorf("catalog entry %s: %w", entryID, err)
	}
	return types.VMCPComponent{
		Name:                    componentName(entry.Manifest.Name),
		MCPCatalogID:            defaultCatalog,
		MCPServerCatalogEntryID: entry.ID,
	}, nil
}

// componentName is a short, stable component name from a display name.
func componentName(display string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(display) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteRune('-')
		}
	}
	return cmpOr(strings.Trim(b.String(), "-"), "server")
}

// subjects turns who should get access into profile subjects, looking users up by email.
func (h *Handler) subjects(ctx context.Context, req api.Context, in accessInput) ([]types.Subject, error) {
	var subjects []types.Subject
	if in.Everyone {
		subjects = append(subjects, types.Subject{Type: types.SubjectTypeSelector, ID: "*"})
	}
	if in.Admins {
		subjects = append(subjects, types.Subject{Type: types.SubjectTypeObotGroup, ID: types.GroupAdmin})
	}
	for _, email := range in.Users {
		users, err := req.GatewayClient.Users(ctx, gtypes.UserQuery{Email: strings.TrimSpace(email)})
		if err != nil {
			return nil, err
		}
		if len(users) == 0 {
			return nil, fmt.Errorf("no Obot user has the email %s", email)
		}
		subjects = append(subjects, types.Subject{Type: types.SubjectTypeUser, ID: fmt.Sprint(users[0].ID)})
	}
	for _, group := range in.Groups {
		if group = strings.TrimSpace(group); group != "" {
			subjects = append(subjects, types.Subject{Type: types.SubjectTypeGroup, ID: group})
		}
	}
	return subjects, nil
}

func profileName(in accessInput) string {
	switch {
	case in.Everyone:
		return "Everyone"
	case in.Admins && len(in.Users)+len(in.Groups) == 0:
		return "Admins"
	case len(in.Users) == 1 && len(in.Groups) == 0 && !in.Admins:
		return in.Users[0]
	}
	return "Access"
}

// vmcpView is a vMCP as the model needs it: components and who can use them.
func (h *Handler) vmcpView(ctx context.Context, req api.Context, vmcp types.VMCP) map[string]any {
	components := make([]map[string]any, 0, len(vmcp.Components))
	for _, c := range vmcp.Components {
		components = append(components, map[string]any{
			"id":             c.ID,
			"name":           c.Name,
			"server":         c.CatalogEntry.Manifest.Name,
			"catalogEntryID": c.MCPServerCatalogEntryID,
			"runtime":        c.CatalogEntry.Manifest.Runtime,
		})
	}
	profiles := make([]map[string]any, 0, len(vmcp.Profiles))
	for _, p := range vmcp.Profiles {
		who := make([]string, 0, len(p.Subjects))
		for _, s := range p.Subjects {
			who = append(who, h.subjectLabel(ctx, req, s))
		}
		access := "all components"
		if !p.Permissions.AllowAllComponents {
			ids := make([]string, 0, len(p.Permissions.AllowedComponents))
			for id := range p.Permissions.AllowedComponents {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			access = "components " + strings.Join(ids, ", ")
		}
		profiles = append(profiles, map[string]any{"name": p.Name, "who": who, "access": access})
	}
	return map[string]any{
		"id":          vmcp.ID,
		"name":        vmcp.DisplayName,
		"description": vmcp.Description,
		"components":  components,
		"profiles":    profiles,
		"connectURL":  h.connectURL(vmcp.ID),
		"url":         h.serverURL + "/vmcps/" + vmcp.ID,
	}
}

func (h *Handler) subjectLabel(ctx context.Context, req api.Context, s types.Subject) string {
	switch s.Type {
	case types.SubjectTypeSelector:
		if s.ID == "*" {
			return "everyone"
		}
	case types.SubjectTypeUser:
		return h.userLabel(ctx, req, s.ID)
	case types.SubjectTypeObotGroup:
		return "Obot role " + s.ID
	case types.SubjectTypeGroup:
		return "group " + s.ID
	}
	return string(s.Type) + " " + s.ID
}

func (h *Handler) addCatalogTools(s *mcp.Server, req api.Context) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_catalog_entries",
		Description: "List the MCP servers in the organization's catalog, which vMCPs are built from.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in queryInput) (*mcp.CallToolResult, any, error) {
		var list types.MCPServerCatalogEntryList
		if err := h.callAPI(ctx, req, "GET", "/mcp-catalogs/"+defaultCatalog+"/entries", nil, nil, &list); err != nil {
			return toolError(err)
		}
		terms := strings.Fields(strings.ToLower(in.Query))
		out := []map[string]any{}
		for _, e := range list.Items {
			haystack := strings.ToLower(e.Manifest.Name + " " + e.Manifest.ShortDescription + " " + e.Manifest.Description)
			if !containsAll(haystack, terms) {
				continue
			}
			out = append(out, map[string]any{"id": e.ID, "name": e.Manifest.Name, "description": e.Manifest.ShortDescription, "runtime": e.Manifest.Runtime, "gitManaged": e.SourceURL != ""})
			if len(out) == 100 {
				break
			}
		}
		return jsonResult(map[string]any{"entries": out, "total": len(list.Items)})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_catalog_entry",
		Description: "Get one catalog entry: how it runs, its configuration and its tools.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, any, error) {
		var entry types.MCPServerCatalogEntry
		if err := h.callAPI(ctx, req, "GET", "/mcp-catalogs/"+defaultCatalog+"/entries/"+url.PathEscape(in.ID), nil, nil, &entry); err != nil {
			return toolError(err)
		}
		tools := make([]string, 0, len(entry.Manifest.ToolPreview))
		for _, t := range entry.Manifest.ToolPreview {
			tools = append(tools, t.Name)
		}
		entry.Manifest.ToolPreview = nil
		return jsonResult(map[string]any{"id": entry.ID, "manifest": entry.Manifest, "tools": tools, "gitManaged": entry.SourceURL != "", "editable": entry.Editable})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "create_catalog_entry",
		Description: "Add an MCP server to the catalog: a remote server by URL, an npx or uvx package, or a container " +
			"image. Then add it to a vMCP so people can use it. Confirm the details with the administrator first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createEntryInput) (*mcp.CallToolResult, any, error) {
		if err := in.Validate(); err != nil {
			return toolError(err)
		}
		var entry types.MCPServerCatalogEntry
		if err := h.callAPI(ctx, req, "POST", "/mcp-catalogs/"+defaultCatalog+"/entries", nil, in.CatalogEntryManifest(), &entry); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"id": entry.ID, "name": entry.Manifest.Name, "message": "Added to the catalog. Add it to a vMCP so people can use it."})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_catalog_entry",
		Description: "Change a catalog entry's name, descriptions or remote URL. Git-managed entries can't be changed here. Confirm with the administrator first.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateEntryInput) (*mcp.CallToolResult, any, error) {
		path := "/mcp-catalogs/" + defaultCatalog + "/entries/" + url.PathEscape(in.ID)
		var entry types.MCPServerCatalogEntry
		if err := h.callAPI(ctx, req, "GET", path, nil, nil, &entry); err != nil {
			return toolError(err)
		}
		m := entry.Manifest
		if v := strings.TrimSpace(in.Name); v != "" {
			m.Name = v
		}
		if in.ShortDescription != "" {
			m.ShortDescription = in.ShortDescription
		}
		if in.Description != "" {
			m.Description = in.Description
		}
		if in.RemoteURL != "" {
			if m.Runtime != types.RuntimeRemote || m.RemoteConfig == nil {
				return toolError(fmt.Errorf("only a remote server has a URL"))
			}
			m.RemoteConfig.FixedURL = in.RemoteURL
		}
		var updated types.MCPServerCatalogEntry
		if err := h.callAPI(ctx, req, "PUT", path, nil, m, &updated); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"id": updated.ID, "name": updated.Manifest.Name, "message": "Saved."})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_catalog_entry",
		Description: "Remove an MCP server from the catalog. Only when the administrator has clearly asked for this entry to be deleted.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, any, error) {
		if err := h.callAPI(ctx, req, "DELETE", "/mcp-catalogs/"+defaultCatalog+"/entries/"+url.PathEscape(in.ID), nil, nil, nil); err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"message": "Deleted catalog entry " + in.ID + "."})
	})
}

func containsAll(haystack string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(haystack, t) {
			return false
		}
	}
	return true
}
