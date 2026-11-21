package collect

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
	gclient "github.com/obot-platform/obot/pkg/gateway/client"
	gtypes "github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/riskregister"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	callTypeToolsCall = "tools/call"
	// findingsPageSize and maxFindingsPages bound how many recent model requests are read
	// for guardrail findings, which can't be filtered in SQL.
	findingsPageSize = 500
	maxFindingsPages = 10
	// maxUnusedSample bounds how many unused tool names are kept on an observation.
	maxUnusedSample = 20
)

// Obot reads Obot's own configuration and audit logs: what is approved, what is granted
// against what is used, what guardrails found, and how long records are kept.
type Obot struct {
	Gateway   *gclient.Client
	Storage   kclient.Client
	ServerURL string
	// NetworkPolicyEnabled and DefaultDenyAllEgress say how hosted MCP server egress is
	// configured for the deployment.
	NetworkPolicyEnabled bool
	DefaultDenyAllEgress bool
	MCPRetentionDays     int
	LLMRetentionDays     int
}

type findingKey struct {
	policyID string
	category string
	outcome  string
}

type findingTally struct {
	policyName string
	count      int
	users      map[string]bool
}

// grantedTool is a tool a profile grants, as the client sees it.
type grantedTool struct {
	prefix  string
	visible string
}

func (*Obot) Kind() types.RiskSourceKind {
	return types.RiskSourceObot
}

func (o *Obot) Collect(ctx context.Context, snapshot *riskregister.Snapshot) (map[string]int, error) {
	gateway := &riskregister.GatewayObservation{WindowDays: snapshot.WindowDays}
	snapshot.Approved.ObotURL = o.ServerURL

	if err := o.catalog(ctx, snapshot, gateway); err != nil {
		return nil, err
	}
	if err := o.users(ctx, snapshot, gateway); err != nil {
		return nil, err
	}
	if err := o.toolGrants(ctx, snapshot, gateway); err != nil {
		return nil, err
	}
	if err := o.guardrails(ctx, snapshot, gateway); err != nil {
		return nil, err
	}

	var exports v1.ScheduledAuditLogExportList
	if err := o.Storage.List(ctx, &exports, kclient.InNamespace(system.DefaultNamespace)); err != nil {
		return nil, err
	}
	gateway.Retention = &riskregister.RetentionObservation{MCPDays: o.MCPRetentionDays, LLMDays: o.LLMRetentionDays}
	for _, export := range exports.Items {
		if export.Spec.Enabled {
			gateway.Retention.ScheduledExports++
		}
	}

	snapshot.Gateway = gateway
	return map[string]int{
		"llmUsers":      len(gateway.LLMUsers),
		"mcpUsers":      len(gateway.MCPUsers),
		"modelRequests": gateway.Guardrails.Requests,
		"vmcpProfiles":  len(gateway.ToolGrants),
		"hostedServers": len(gateway.HostedServers),
	}, nil
}

// catalog records what Obot's catalog approves, and the servers Obot hosts with whether their
// egress is restricted.
func (o *Obot) catalog(ctx context.Context, snapshot *riskregister.Snapshot, gateway *riskregister.GatewayObservation) error {
	var entries v1.MCPServerCatalogEntryList
	if err := o.Storage.List(ctx, &entries, kclient.InNamespace(system.DefaultNamespace)); err != nil {
		return err
	}
	for _, entry := range entries.Items {
		manifest := entry.Spec.Manifest
		if manifest.RemoteConfig != nil {
			approve(&snapshot.Approved, manifest.RemoteConfig.FixedURL, manifest.RemoteConfig.Hostname)
		}
		approvePackages(&snapshot.Approved, manifest.NPXConfig, manifest.UVXConfig)
	}

	var servers v1.MCPServerList
	if err := o.Storage.List(ctx, &servers, kclient.InNamespace(system.DefaultNamespace)); err != nil {
		return err
	}
	for _, server := range servers.Items {
		manifest := server.Spec.Manifest
		if manifest.RemoteConfig != nil {
			approve(&snapshot.Approved, manifest.RemoteConfig.URL, manifest.RemoteConfig.Hostname)
		}
		approvePackages(&snapshot.Approved, manifest.NPXConfig, manifest.UVXConfig)

		var (
			domains []string
			deny    *bool
		)
		switch {
		case manifest.Runtime == types.RuntimeNPX && manifest.NPXConfig != nil:
			domains, deny = manifest.NPXConfig.EgressDomains, manifest.NPXConfig.DenyAllEgress
		case manifest.Runtime == types.RuntimeUVX && manifest.UVXConfig != nil:
			domains, deny = manifest.UVXConfig.EgressDomains, manifest.UVXConfig.DenyAllEgress
		case manifest.Runtime == types.RuntimeContainerized && manifest.ContainerizedConfig != nil:
			domains, deny = manifest.ContainerizedConfig.EgressDomains, manifest.ContainerizedConfig.DenyAllEgress
		default:
			// Remote servers and vMCPs aren't hosted, so egress rules don't apply.
			continue
		}
		denyAll := o.DefaultDenyAllEgress && len(domains) == 0
		if deny != nil {
			denyAll = *deny
		}
		name := manifest.Name
		if name == "" {
			name = server.Name
		}
		gateway.HostedServers = append(gateway.HostedServers, riskregister.HostedServerObservation{
			ID:               server.Name,
			Name:             name,
			Runtime:          string(manifest.Runtime),
			EgressRestricted: o.NetworkPolicyEnabled && (denyAll || len(domains) > 0),
		})
	}
	return nil
}

// users records who was seen on each gateway in the window, by email, so vendor activity can
// be compared against it.
func (o *Obot) users(ctx context.Context, snapshot *riskregister.Snapshot, gateway *riskregister.GatewayObservation) error {
	all, err := o.Gateway.Users(ctx, gtypes.UserQuery{})
	if err != nil {
		return err
	}
	emails := make(map[string]string, len(all))
	for _, user := range all {
		if user.Email != "" {
			emails[strconv.FormatUint(uint64(user.ID), 10)] = strings.ToLower(user.Email)
		}
	}

	usage, err := o.Gateway.TokenUsageByUser(ctx, windowStart(snapshot), snapshot.CollectedAt)
	if err != nil {
		return err
	}
	llm := map[string]bool{}
	for _, row := range usage {
		if email := emails[row.UserID]; email != "" {
			llm[email] = true
		}
	}
	gateway.LLMUsers = sortedKeys(llm)

	ids, err := o.Gateway.GetAuditLogFilterOptions(ctx, "user_id", gclient.MCPAuditLogOptions{
		SourceTypes: []types.AuditLogSourceType{types.AuditLogSourceTypeMCP},
		StartTime:   windowStart(snapshot),
	})
	if err != nil {
		return err
	}
	mcp := map[string]bool{}
	for _, id := range ids {
		if email := emails[id]; email != "" {
			mcp[email] = true
		}
	}
	gateway.MCPUsers = sortedKeys(mcp)
	return nil
}

// toolGrants compares, for each profile of each shared vMCP, the tools the profile grants
// with the tools anyone called on that vMCP in the window. Calls aren't attributed to a
// profile, so a tool counts as used when anyone used it.
func (o *Obot) toolGrants(ctx context.Context, snapshot *riskregister.Snapshot, gateway *riskregister.GatewayObservation) error {
	var vmcps v1.VMCPList
	if err := o.Storage.List(ctx, &vmcps, kclient.InNamespace(system.DefaultNamespace)); err != nil {
		return err
	}
	for _, vmcp := range vmcps.Items {
		if vmcp.Spec.UserID != "" {
			continue
		}
		called, err := o.Gateway.GetAuditLogFilterOptions(ctx, "call_identifier", gclient.MCPAuditLogOptions{
			SourceTypes: []types.AuditLogSourceType{types.AuditLogSourceTypeMCP},
			MCPID:       []string{vmcp.Name},
			CallType:    []string{callTypeToolsCall},
			StartTime:   windowStart(snapshot),
		})
		if err != nil {
			return err
		}
		for _, profile := range vmcp.Spec.Manifest.Profiles {
			granted, countable := grantedTools(vmcp.Spec.Manifest, profile)
			if !countable || len(granted) == 0 {
				continue
			}
			observation := riskregister.ToolGrantObservation{
				VMCPID:   vmcp.Name,
				VMCPName: vmcp.Spec.Manifest.DisplayName,
				Profile:  profile.Name,
				Granted:  len(granted),
			}
			for _, tool := range granted {
				if tool.usedIn(called) {
					observation.Used++
				} else if len(observation.Unused) < maxUnusedSample {
					observation.Unused = append(observation.Unused, tool.visible)
				}
			}
			gateway.ToolGrants = append(gateway.ToolGrants, observation)
		}
	}
	return nil
}

// usedIn reports whether the tool appears among the called tool names. The component's
// prefix is joined to the name by the gateway, so a prefixed tool matches a called name that
// starts with the prefix and ends with the tool's name.
func (t grantedTool) usedIn(called []string) bool {
	for _, name := range called {
		if name == t.prefix+t.visible || (t.prefix != "" && strings.HasPrefix(name, t.prefix) && strings.HasSuffix(name, t.visible)) {
			return true
		}
	}
	return false
}

// grantedTools lists the tools a profile grants. It returns false when the grant includes a
// component whose tools aren't listed in the manifest, since those can't be counted.
func grantedTools(manifest types.VMCPManifest, profile types.VMCPProfile) ([]grantedTool, bool) {
	var tools []grantedTool
	for _, component := range manifest.Components {
		set, ok := profile.Permissions.AllowedComponents[component.ID]
		if !ok && !profile.Permissions.AllowAllComponents {
			continue
		}
		overrides := map[string]types.ToolOverride{}
		for _, override := range component.ToolOverrides {
			overrides[override.Name] = override
		}

		names := set.AllowedTools
		if !ok || names == nil {
			// Every tool the component exposes is granted. Without a tool list on the
			// component there is nothing to count.
			if len(component.ToolOverrides) == 0 {
				return nil, false
			}
			names = nil
			for _, override := range component.ToolOverrides {
				if override.Enabled {
					names = append(names, override.Name)
				}
			}
		}
		for _, name := range names {
			visible := name
			if override, ok := overrides[name]; ok && override.OverrideName != "" {
				visible = override.OverrideName
			}
			tools = append(tools, grantedTool{prefix: component.ToolPrefix, visible: visible})
		}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].prefix+tools[i].visible < tools[j].prefix+tools[j].visible })
	return tools, true
}

// guardrails records which guardrail types are applied to model traffic and what the
// applied guardrails found in recent requests.
func (o *Obot) guardrails(ctx context.Context, snapshot *riskregister.Snapshot, gateway *riskregister.GatewayObservation) error {
	definitions, err := o.Gateway.ListGuardrails(ctx)
	if err != nil {
		return err
	}
	kinds := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		kinds[definition.ID] = definition.Type
	}
	policies, err := o.Gateway.ListGuardrailPolicies(ctx)
	if err != nil {
		return err
	}
	applied := map[string]bool{}
	for _, policy := range policies {
		if !policy.Enabled || policy.Target != gtypes.GuardrailTargetLLM {
			continue
		}
		for _, id := range policy.GuardrailIDs {
			if kind := kinds[id]; kind != "" {
				applied[kind] = true
			}
		}
	}
	gateway.Guardrails.AppliedTypes = sortedKeys(applied)

	tallies := map[findingKey]*findingTally{}
	for page := range maxFindingsPages {
		logs, total, err := o.Gateway.GetLLMAuditLogs(ctx, gclient.LLMAuditLogOptions{
			HideModelsRequests: true,
			StartTime:          windowStart(snapshot),
			Limit:              findingsPageSize,
			Offset:             page * findingsPageSize,
		})
		if err != nil {
			return err
		}
		gateway.Guardrails.Requests = int(total)
		for _, log := range logs {
			for _, finding := range log.GuardrailFindings {
				categories := finding.Categories
				if len(categories) == 0 {
					categories = []string{finding.GuardrailType}
				}
				for _, category := range categories {
					key := findingKey{policyID: finding.PolicyID, category: category, outcome: finding.Outcome}
					if tallies[key] == nil {
						tallies[key] = &findingTally{policyName: finding.PolicyName, users: map[string]bool{}}
					}
					tallies[key].count++
					tallies[key].users[log.UserID] = true
				}
			}
		}
		if len(logs) < findingsPageSize {
			break
		}
	}
	for key, tally := range tallies {
		gateway.Guardrails.Findings = append(gateway.Guardrails.Findings, riskregister.GuardrailFindingObservation{
			Category:   key.category,
			PolicyID:   key.policyID,
			PolicyName: tally.policyName,
			Outcome:    key.outcome,
			Count:      tally.count,
			Users:      len(tally.users),
		})
	}
	sort.Slice(gateway.Guardrails.Findings, func(i, j int) bool {
		a, b := gateway.Guardrails.Findings[i], gateway.Guardrails.Findings[j]
		return a.PolicyID+a.Category+a.Outcome < b.PolicyID+b.Category+b.Outcome
	})
	return nil
}

func approve(approved *riskregister.ApprovedServers, serverURL, hostname string) {
	if serverURL != "" {
		approved.URLs = append(approved.URLs, serverURL)
	}
	if hostname != "" {
		approved.Hostnames = append(approved.Hostnames, hostname)
	}
}

func approvePackages(approved *riskregister.ApprovedServers, npx *types.NPXRuntimeConfig, uvx *types.UVXRuntimeConfig) {
	if npx != nil && npx.Package != "" {
		approved.Packages = append(approved.Packages, riskregister.ServerPackage{Source: types.AllowlistServerPackageSourceNPM, Name: stripPackageVersion(npx.Package, true)})
	}
	if uvx != nil && uvx.Package != "" {
		approved.Packages = append(approved.Packages, riskregister.ServerPackage{Source: types.AllowlistServerPackageSourcePyPI, Name: stripPackageVersion(uvx.Package, false)})
	}
}

// stripPackageVersion drops a version from a catalog package reference, so a pinned catalog
// entry still approves the package by name.
func stripPackageVersion(pkg string, npm bool) string {
	if npm {
		if at := strings.LastIndex(pkg, "@"); at > 0 {
			return pkg[:at]
		}
		return pkg
	}
	for _, sep := range []string{"[", "==", ">=", "<=", "~=", "@"} {
		if i := strings.Index(pkg, sep); i > 0 {
			pkg = pkg[:i]
		}
	}
	return pkg
}
