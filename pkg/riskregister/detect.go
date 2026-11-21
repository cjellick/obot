package riskregister

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
)

const (
	TypeUnapprovedMCPServer   = "unapproved-mcp-server"
	TypeClientNotEnforced     = "client-without-enforcement"
	TypeUnusedToolGrants      = "unused-tool-grants"
	TypeUnusedModelGrants     = "unused-model-grants"
	TypeSecretsNotChecked     = "secrets-not-checked"
	TypeSecretsInTraffic      = "secrets-in-model-traffic"
	TypePersonalNotChecked    = "personal-data-not-checked"
	TypeSensitiveInTraffic    = "sensitive-data-in-model-traffic"
	TypeUnrestrictedEgress    = "hosted-server-unrestricted-egress"
	TypeRecordsExpire         = "audit-records-expire"
	TypeUseOutsideGateway     = "ai-use-outside-gateway"
	TypeAppOutsideGateway     = "ai-app-outside-gateway"
	TypeConnectorOutside      = "connector-outside-gateway"
	TypeLongLivedProviderKeys = "long-lived-provider-keys"
	TypeRepoAgentSettings     = "repository-agent-settings"

	TreatmentSentryAllowlist  = "sentry-allowlist"
	TreatmentVMCPProfile      = "vmcp-profile"
	TreatmentModelPolicy      = "model-access-policy"
	TreatmentGuardrail        = "guardrail"
	TreatmentEgress           = "mcp-egress"
	TreatmentRetention        = "audit-retention"
	TreatmentRouteViaGateway  = "llm-gateway"
	TreatmentPublishVMCP      = "vmcp"
	TreatmentNone             = "none"
	guardrailTypeSecrets      = "secrets"
	guardrailCategorySecret   = "secret"
	guardrailOutcomeLogged    = "logged"
	minEvidenceRetentionDays  = 180
	providerKeyMaxAgeDays     = 90
	maxEvidencePerFinding     = 20
	manyDevices               = 5
	manyUsers                 = 5
	frameworkSupplyChain      = "OWASP LLM04:2026 Supply Chain"
	frameworkAgenticSupply    = "OWASP ASI04 Agentic Supply Chain"
	frameworkExcessiveAgency  = "OWASP LLM03:2026 Excessive Agency"
	frameworkPrivilegeAbuse   = "OWASP ASI03 Identity and Privilege Abuse"
	frameworkSensitiveInfo    = "OWASP LLM02:2026 Sensitive Information Disclosure"
	frameworkToolMisuse       = "OWASP ASI02 Tool Misuse"
	frameworkInventory        = "NIST AI RMF GOVERN 1.6"
	frameworkThirdParty       = "NIST AI RMF GOVERN 6.1"
	frameworkEventLogs        = "ISO/IEC 42001 A.6.2.8"
	frameworkPostDeployment   = "NIST AI RMF MANAGE 4.1"
	productClaudeCode         = "Claude Code"
	allowlistDescriptionAllow = "Turn on tool-call enforcement so this server is blocked on supported clients, or approve it by adding the allow rule below to the device configuration."
	allowlistDescriptionBlock = "Turn on tool-call enforcement so this server is blocked on supported clients. It runs from a bare command, which an allow rule can't identify, so it can't be approved as it is."
)

var (
	// personalDataGuardrails are the guardrail types that look for personal data.
	personalDataGuardrails = []string{"regex", "presidio", "azure-language-pii", "aws-bedrock", "google-model-armor", "lakera", "crowdstrike-aidr"}

	severityRank = map[types.RiskSeverity]int{
		types.RiskSeverityHigh:   0,
		types.RiskSeverityMedium: 1,
		types.RiskSeverityLow:    2,
	}
)

// Finding is a risk the detectors found in a snapshot. Type and Key together identify the
// same condition from one run to the next.
type Finding struct {
	Type       string
	Key        string
	Segment    types.RiskSegment
	Severity   types.RiskSeverity
	Title      string
	Summary    string
	Sources    []types.RiskSourceKind
	Evidence   []types.RiskEvidence
	Metrics    map[string]int
	Treatment  *types.RiskTreatment
	Frameworks []string
}

// Fingerprint identifies the finding across runs.
func (f Finding) Fingerprint() string {
	return f.Type + ":" + f.Key
}

// Detect runs every fixed check over a snapshot and returns the findings, most severe first.
func Detect(snapshot Snapshot) []Finding {
	var findings []Finding
	findings = append(findings, detectMCPServers(snapshot)...)
	findings = append(findings, detectClients(snapshot)...)
	findings = append(findings, detectConnectors(snapshot)...)
	findings = append(findings, detectApps(snapshot)...)
	findings = append(findings, detectActivity(snapshot)...)
	findings = append(findings, detectProviderKeys(snapshot)...)
	findings = append(findings, detectRepoSettings(snapshot)...)
	if snapshot.Gateway != nil {
		findings = append(findings, detectGrants(*snapshot.Gateway)...)
		findings = append(findings, detectGuardrails(*snapshot.Gateway)...)
		findings = append(findings, detectEgress(*snapshot.Gateway)...)
		findings = append(findings, detectRetention(*snapshot.Gateway)...)
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if a, b := severityRank[findings[i].Severity], severityRank[findings[j].Severity]; a != b {
			return a < b
		}
		return findings[i].Fingerprint() < findings[j].Fingerprint()
	})
	return findings
}

func detectMCPServers(snapshot Snapshot) []Finding {
	var findings []Finding
	for _, server := range snapshot.MCPServers {
		if approved, _ := snapshot.Approved.Match(server); approved {
			continue
		}
		onDevices := server.Source == types.RiskSourceSentry

		identity := serverIdentity(server)
		finding := Finding{
			Type:     TypeUnapprovedMCPServer,
			Key:      string(server.Source) + ":" + server.Key,
			Segment:  types.RiskSegmentCodingAgent,
			Severity: types.RiskSeverityMedium,
			Sources:  []types.RiskSourceKind{server.Source},
			Metrics:  map[string]int{},
			Evidence: []types.RiskEvidence{{
				Source: server.Source,
				Kind:   "mcp-server",
				Ref:    server.Key,
				Detail: identity,
			}},
			Frameworks: []string{frameworkAgenticSupply, frameworkSupplyChain},
		}
		if onDevices {
			finding.Title = fmt.Sprintf("Unapproved MCP server %q on %s", server.Name, plural(server.Devices, "device"))
			finding.Summary = fmt.Sprintf("%s is configured on %s for %s. It isn't in Obot's catalog and no device allow rule covers it.",
				identity, plural(server.Devices, "device"), plural(server.Users, "user"))
			finding.Metrics["devices"] = server.Devices
			finding.Metrics["users"] = server.Users
			if server.Devices >= manyDevices {
				finding.Severity = types.RiskSeverityHigh
			}
		} else {
			finding.Title = fmt.Sprintf("MCP server %q is defined in a repository", server.Name)
			finding.Summary = fmt.Sprintf("%s is defined in %s. Anyone who opens the repository with an AI coding agent can be offered it. It isn't in Obot's catalog and no device allow rule covers it.",
				identity, server.Location)
		}
		finding.Treatment = serverTreatment(server, snapshot.WindowDays)
		findings = append(findings, finding)
	}
	return findings
}

// serverTreatment drafts the Sentry allow rule that would approve a server, with what
// enforcement would have blocked in the replay window.
func serverTreatment(server MCPServerObservation, windowDays int) *types.RiskTreatment {
	treatment := &types.RiskTreatment{Kind: TreatmentSentryAllowlist, Description: allowlistDescriptionAllow}

	var rule *types.AllowlistServer
	if pkg, ok := PackageOf(server.Command, server.Args); ok {
		rule = &types.AllowlistServer{Package: &types.AllowlistServerPackage{Source: pkg.Source, Name: pkg.Name}}
	} else if server.URL != "" {
		rule = &types.AllowlistServer{URL: server.URL}
	}
	if rule == nil {
		treatment.Description = allowlistDescriptionBlock
	} else if payload, err := json.Marshal(rule); err == nil {
		treatment.Payload = payload
	}

	if server.Source == types.RiskSourceSentry {
		treatment.Replay = &types.RiskReplay{
			WindowDays: windowDays,
			Events:     server.Calls,
			Users:      server.CallUsers,
			Note:       "Tool calls devices reported to a server of this name. Enforcement would have blocked them until the server was approved.",
		}
	}
	return treatment
}

func detectClients(snapshot Snapshot) []Finding {
	var findings []Finding
	for _, observed := range snapshot.Clients {
		client := lookupClient(observed.Name)
		if client.Enforced {
			continue
		}
		severity := types.RiskSeverityLow
		if observed.Devices >= manyDevices {
			severity = types.RiskSeverityMedium
		}
		visibility := "Obot Sentry doesn't record its tool calls and can't block them."
		if client.Audited {
			visibility = "Obot Sentry records its tool calls but can't block them."
		}
		findings = append(findings, Finding{
			Type:     TypeClientNotEnforced,
			Key:      strings.ToLower(observed.Name),
			Segment:  client.Segment,
			Severity: severity,
			Title:    fmt.Sprintf("%s on %s is outside tool-call enforcement", client.Display, plural(observed.Devices, "device")),
			Summary:  fmt.Sprintf("%s is installed on %s for %s. %s", client.Display, plural(observed.Devices, "device"), plural(observed.Users, "user"), visibility),
			Sources:  []types.RiskSourceKind{observed.Source},
			Metrics:  map[string]int{"devices": observed.Devices, "users": observed.Users},
			Evidence: []types.RiskEvidence{{Source: observed.Source, Kind: "device-client", Ref: observed.Name}},
			Treatment: &types.RiskTreatment{
				Kind:        TreatmentNone,
				Description: "Obot has no policy that blocks an AI client. Decide whether this client is allowed and record the decision here. Removing it is a device management change.",
			},
			Frameworks: []string{frameworkInventory, frameworkToolMisuse},
		})
	}
	return findings
}

func detectConnectors(snapshot Snapshot) []Finding {
	var findings []Finding
	for _, connector := range snapshot.Connectors {
		if connector.URL != "" {
			if approved, _ := snapshot.Approved.Match(MCPServerObservation{URL: connector.URL}); approved {
				continue
			}
		}
		identity := connector.Name
		if connector.URL != "" {
			identity = fmt.Sprintf("%s (%s)", connector.Name, connector.URL)
		}
		severity := types.RiskSeverityMedium
		if connector.Users >= manyUsers {
			severity = types.RiskSeverityHigh
		}
		findings = append(findings, Finding{
			Type:     TypeConnectorOutside,
			Key:      string(connector.Source) + ":" + strings.ToLower(connector.Name+"|"+connector.URL),
			Segment:  types.RiskSegmentBusinessUser,
			Severity: severity,
			Title:    fmt.Sprintf("Connector %q was attached inside Claude by %s", connector.Name, plural(connector.Users, "person")),
			Summary:  fmt.Sprintf("%s was connected in the vendor's own app, so its tool calls don't pass through Obot and no Obot policy applies to them.", identity),
			Sources:  []types.RiskSourceKind{connector.Source},
			Metrics:  map[string]int{"users": connector.Users, "events": connector.Events},
			Evidence: []types.RiskEvidence{{Source: connector.Source, Kind: "connector", Ref: connector.Name, Detail: connector.URL}},
			Treatment: &types.RiskTreatment{
				Kind:        TreatmentPublishVMCP,
				Description: "Publish this server through an Obot vMCP with a profile for the people who need it, then restrict custom connectors in the vendor's admin settings so the vMCP is the only route.",
			},
			Frameworks: []string{frameworkThirdParty, frameworkToolMisuse},
		})
	}
	return findings
}

func detectApps(snapshot Snapshot) []Finding {
	var findings []Finding
	for _, app := range snapshot.Apps {
		if app.Match == nil || !app.Active || app.AssignedUsers == 0 {
			continue
		}
		severity := types.RiskSeverityLow
		if app.AssignedUsers >= 4*manyUsers {
			severity = types.RiskSeverityMedium
		}
		detail := "Recognized from the built-in list of AI tools."
		if app.Match.ByModel {
			detail = "Recognized as an AI tool by a model, from the application's name."
		}
		findings = append(findings, Finding{
			Type:     TypeAppOutsideGateway,
			Key:      string(app.Source) + ":" + app.ID,
			Segment:  app.Match.Segment,
			Severity: severity,
			Title:    fmt.Sprintf("%s is assigned to %s in the identity provider", app.Label, plural(app.AssignedUsers, "person")),
			Summary:  fmt.Sprintf("%s (%s) is an AI tool people sign in to directly. Obot doesn't see what they send it or what it connects to.", app.Label, app.Match.Name),
			Sources:  []types.RiskSourceKind{app.Source},
			Metrics:  map[string]int{"users": app.AssignedUsers},
			Evidence: []types.RiskEvidence{{Source: app.Source, Kind: "identity-provider-app", Ref: app.ID, Detail: detail}},
			Treatment: &types.RiskTreatment{
				Kind:        TreatmentNone,
				Description: "Obot can't route a vendor's own app. Record whether this tool is approved and for whom. Where the vendor offers an activity or compliance API, connect it as a source so its use shows up here.",
			},
			Frameworks: []string{frameworkInventory, frameworkThirdParty},
		})
	}
	return findings
}

func detectActivity(snapshot Snapshot) []Finding {
	var gatewayUsers []string
	if snapshot.Gateway != nil {
		gatewayUsers = snapshot.Gateway.LLMUsers
	}

	var findings []Finding
	for _, activity := range snapshot.Activity {
		outside := 0
		for _, user := range activity.Users {
			if !slices.Contains(gatewayUsers, user) {
				outside++
			}
		}
		if outside == 0 {
			continue
		}
		severity := types.RiskSeverityLow
		if outside >= manyUsers {
			severity = types.RiskSeverityMedium
		}
		treatment := &types.RiskTreatment{
			Kind:        TreatmentNone,
			Description: "Obot can't route this product. Keep this source connected so its use stays visible, and record whether the use is approved.",
		}
		if activity.Product == productClaudeCode {
			treatment = &types.RiskTreatment{
				Kind:        TreatmentRouteViaGateway,
				Description: "Point Claude Code at the Obot LLM gateway through managed settings, so requests carry an Obot identity, are logged, and fall under model access and guardrail policies.",
			}
		}
		findings = append(findings, Finding{
			Type:     TypeUseOutsideGateway,
			Key:      string(activity.Source) + ":" + strings.ToLower(activity.Product),
			Segment:  activity.Segment,
			Severity: severity,
			Title:    fmt.Sprintf("%s used %s outside the Obot gateway", plural(outside, "person"), activity.Product),
			Summary: fmt.Sprintf("%s had activity in %s in the last %d days (%d events). %d of them have no requests on the Obot LLM gateway in the same period.",
				plural(len(activity.Users), "person"), activity.Product, snapshot.WindowDays, activity.Events, outside),
			Sources:    []types.RiskSourceKind{activity.Source},
			Metrics:    map[string]int{"users": len(activity.Users), "usersOutsideGateway": outside, "events": activity.Events},
			Evidence:   []types.RiskEvidence{{Source: activity.Source, Kind: "activity-feed", Ref: activity.Product, Detail: "Last seen " + activity.LastSeen.UTC().Format(time.DateOnly)}},
			Treatment:  treatment,
			Frameworks: []string{frameworkInventory, frameworkPostDeployment},
		})
	}
	return findings
}

func detectProviderKeys(snapshot Snapshot) []Finding {
	byProvider := map[string][]APIKeyObservation{}
	for _, key := range snapshot.APIKeys {
		if key.Active {
			byProvider[key.Provider] = append(byProvider[key.Provider], key)
		}
	}

	var findings []Finding
	for provider, keys := range byProvider {
		sort.Slice(keys, func(i, j int) bool { return keys[i].CreatedAt.Before(keys[j].CreatedAt) })
		var (
			old      int
			evidence []types.RiskEvidence
			source   = keys[0].Source
		)
		for _, key := range keys {
			age := int(snapshot.CollectedAt.Sub(key.CreatedAt).Hours() / 24)
			if age <= providerKeyMaxAgeDays {
				continue
			}
			old++
			if len(evidence) < maxEvidencePerFinding {
				evidence = append(evidence, types.RiskEvidence{
					Source: key.Source,
					Kind:   "provider-api-key",
					Ref:    key.ID,
					Detail: fmt.Sprintf("%q, %d days old, %s", key.Name, age, key.Scope),
				})
			}
		}
		if old == 0 {
			continue
		}
		severity := types.RiskSeverityMedium
		if old >= manyUsers {
			severity = types.RiskSeverityHigh
		}
		findings = append(findings, Finding{
			Type:     TypeLongLivedProviderKeys,
			Key:      strings.ToLower(provider),
			Segment:  types.RiskSegmentAPIWorkload,
			Severity: severity,
			Title:    fmt.Sprintf("%s older than %d days", plural(old, provider+" API key"), providerKeyMaxAgeDays),
			Summary: fmt.Sprintf("%d of %s are more than %d days old. Each one calls models directly, outside Obot's logging and policies, for as long as it stays valid.",
				old, plural(len(keys), "active "+provider+" API key"), providerKeyMaxAgeDays),
			Sources:  []types.RiskSourceKind{source},
			Metrics:  map[string]int{"activeKeys": len(keys), "oldKeys": old},
			Evidence: evidence,
			Treatment: &types.RiskTreatment{
				Kind:        TreatmentRouteViaGateway,
				Description: "Keep the provider key in Obot and hand workloads scoped Obot API keys, so each one has an owner, a log and a model policy. Then rotate or deactivate the keys listed here.",
			},
			Frameworks: []string{frameworkPrivilegeAbuse, frameworkInventory},
		})
	}
	return findings
}

func detectRepoSettings(snapshot Snapshot) []Finding {
	var findings []Finding
	for _, setting := range snapshot.RepoSettings {
		severity := types.RiskSeverityMedium
		if setting.Setting == RepoSettingBypassPermissions {
			severity = types.RiskSeverityHigh
		}
		findings = append(findings, Finding{
			Type:     TypeRepoAgentSettings,
			Key:      strings.ToLower(setting.Repository + "|" + setting.File + "|" + setting.Setting),
			Segment:  types.RiskSegmentCodingAgent,
			Severity: severity,
			Title:    fmt.Sprintf("%s widens what an agent may do without asking", setting.Repository),
			Summary:  fmt.Sprintf("%s in %s: %s Anyone who opens the repository with that agent gets this setting.", setting.File, setting.Repository, setting.Detail),
			Sources:  []types.RiskSourceKind{setting.Source},
			Evidence: []types.RiskEvidence{{Source: setting.Source, Kind: "repository-file", Ref: setting.Repository + ":" + setting.File, Detail: setting.Setting}},
			Treatment: &types.RiskTreatment{
				Kind:        TreatmentNone,
				Description: "Remove the setting from the repository, or override it with the agent's managed settings, which Obot Sentry installs on enrolled devices.",
			},
			Frameworks: []string{frameworkExcessiveAgency, frameworkAgenticSupply},
		})
	}
	return findings
}

func detectGrants(gateway GatewayObservation) []Finding {
	var findings []Finding
	for _, grant := range gateway.ToolGrants {
		if !mostlyUnused(grant.Granted, grant.Used) {
			continue
		}
		payload, _ := json.Marshal(map[string]any{"vmcpID": grant.VMCPID, "profile": grant.Profile, "removeTools": grant.Unused})
		findings = append(findings, Finding{
			Type:     TypeUnusedToolGrants,
			Key:      grant.VMCPID + "|" + grant.Profile,
			Segment:  types.RiskSegmentPlatform,
			Severity: types.RiskSeverityMedium,
			Title:    fmt.Sprintf("%q profile %q grants %d tools; %d were used", grant.VMCPName, grant.Profile, grant.Granted, grant.Used),
			Summary: fmt.Sprintf("In the last %d days, %d of the %d tools this profile grants were called. The other %d are access nobody is using.",
				gateway.WindowDays, grant.Used, grant.Granted, grant.Granted-grant.Used),
			Sources:  []types.RiskSourceKind{types.RiskSourceObot},
			Metrics:  map[string]int{"granted": grant.Granted, "used": grant.Used},
			Evidence: []types.RiskEvidence{{Source: types.RiskSourceObot, Kind: "vmcp-profile", Ref: grant.VMCPID, Detail: grant.Profile}},
			Treatment: &types.RiskTreatment{
				Kind:        TreatmentVMCPProfile,
				Description: "Narrow the profile to the tools that were used. People who need another tool can ask for it.",
				Payload:     payload,
				Replay:      &types.RiskReplay{WindowDays: gateway.WindowDays, Note: "No call in the window used a tool this change removes."},
			},
			Frameworks: []string{frameworkExcessiveAgency, frameworkPrivilegeAbuse},
		})
	}
	for _, grant := range gateway.ModelGrants {
		if !mostlyUnused(grant.Granted, grant.Used) {
			continue
		}
		payload, _ := json.Marshal(map[string]any{"policyID": grant.PolicyID, "removeModels": grant.Unused})
		findings = append(findings, Finding{
			Type:     TypeUnusedModelGrants,
			Key:      grant.PolicyID,
			Segment:  types.RiskSegmentPlatform,
			Severity: types.RiskSeverityLow,
			Title:    fmt.Sprintf("Profile %q grants %d models; %d were used", grant.PolicyName, grant.Granted, grant.Used),
			Summary: fmt.Sprintf("In the last %d days, %d of the %d models this profile grants were requested.",
				gateway.WindowDays, grant.Used, grant.Granted),
			Sources:  []types.RiskSourceKind{types.RiskSourceObot},
			Metrics:  map[string]int{"granted": grant.Granted, "used": grant.Used},
			Evidence: []types.RiskEvidence{{Source: types.RiskSourceObot, Kind: "model-access-policy", Ref: grant.PolicyID}},
			Treatment: &types.RiskTreatment{
				Kind:        TreatmentModelPolicy,
				Description: "Narrow the profile to the models that were requested.",
				Payload:     payload,
				Replay:      &types.RiskReplay{WindowDays: gateway.WindowDays, Note: "No request in the window used a model this change removes."},
			},
			Frameworks: []string{frameworkExcessiveAgency},
		})
	}
	return findings
}

// mostlyUnused reports whether a grant is wide enough, and unused enough, to be worth an
// entry: at least five unused items making up at least half of the grant.
func mostlyUnused(granted, used int) bool {
	unused := granted - used
	return unused >= 5 && unused*2 >= granted
}

func detectGuardrails(gateway GatewayObservation) []Finding {
	guardrails := gateway.Guardrails
	if guardrails.Requests == 0 {
		return nil
	}

	var findings []Finding
	if !slices.Contains(guardrails.AppliedTypes, guardrailTypeSecrets) {
		payload, _ := json.Marshal(map[string]any{"guardrailType": guardrailTypeSecrets, "action": "log", "applyTo": "input"})
		findings = append(findings, Finding{
			Type:     TypeSecretsNotChecked,
			Key:      "llm-gateway",
			Segment:  types.RiskSegmentPlatform,
			Severity: types.RiskSeverityMedium,
			Title:    "Model requests aren't checked for secrets",
			Summary: fmt.Sprintf("The LLM gateway carried %s in the last %d days and no enabled guardrail looks for API keys, tokens or passwords in them.",
				plural(guardrails.Requests, "request"), gateway.WindowDays),
			Sources:  []types.RiskSourceKind{types.RiskSourceObot},
			Metrics:  map[string]int{"requests": guardrails.Requests},
			Evidence: []types.RiskEvidence{{Source: types.RiskSourceObot, Kind: "guardrail-policies", Detail: "No enabled policy applies a secrets guardrail."}},
			Treatment: &types.RiskTreatment{
				Kind:        TreatmentGuardrail,
				Description: "Apply Secrets Detection to model input in the profile for everyone, in log mode first. The next collection will show what it finds, and you can then switch it to redact.",
				Payload:     payload,
			},
			Frameworks: []string{frameworkSensitiveInfo},
		})
	}
	if !slices.ContainsFunc(guardrails.AppliedTypes, func(t string) bool { return slices.Contains(personalDataGuardrails, t) }) {
		payload, _ := json.Marshal(map[string]any{"guardrailType": "regex", "action": "log", "applyTo": "input"})
		findings = append(findings, Finding{
			Type:     TypePersonalNotChecked,
			Key:      "llm-gateway",
			Segment:  types.RiskSegmentPlatform,
			Severity: types.RiskSeverityLow,
			Title:    "Model requests aren't checked for personal data",
			Summary: fmt.Sprintf("The LLM gateway carried %s in the last %d days and no enabled guardrail looks for personal data in them.",
				plural(guardrails.Requests, "request"), gateway.WindowDays),
			Sources:  []types.RiskSourceKind{types.RiskSourceObot},
			Metrics:  map[string]int{"requests": guardrails.Requests},
			Evidence: []types.RiskEvidence{{Source: types.RiskSourceObot, Kind: "guardrail-policies", Detail: "No enabled policy applies a personal-data guardrail."}},
			Treatment: &types.RiskTreatment{
				Kind:        TreatmentGuardrail,
				Description: "Apply a personal-data guardrail (Custom Regex with the built-in patterns, or Presidio) to model input in log mode first.",
				Payload:     payload,
			},
			Frameworks: []string{frameworkSensitiveInfo},
		})
	}

	for _, found := range guardrails.Findings {
		if found.Outcome != guardrailOutcomeLogged || found.Count == 0 {
			continue
		}
		finding := Finding{
			Type:       TypeSensitiveInTraffic,
			Key:        found.PolicyID + "|" + found.Category,
			Segment:    types.RiskSegmentPlatform,
			Severity:   types.RiskSeverityMedium,
			Title:      fmt.Sprintf("%q data was found in model requests %s and only logged", found.Category, plural(found.Count, "time")),
			Sources:    []types.RiskSourceKind{types.RiskSourceObot},
			Metrics:    map[string]int{"findings": found.Count, "users": found.Users},
			Evidence:   []types.RiskEvidence{{Source: types.RiskSourceObot, Kind: "guardrail-finding", Ref: found.PolicyID, Detail: found.Category}},
			Frameworks: []string{frameworkSensitiveInfo},
		}
		if found.Category == guardrailCategorySecret {
			finding.Type = TypeSecretsInTraffic
			finding.Severity = types.RiskSeverityHigh
			finding.Title = fmt.Sprintf("Secrets were sent to models %s and only logged", plural(found.Count, "time"))
		}
		finding.Summary = fmt.Sprintf("Guardrail policy %q found this in requests from %s over the last %d days. The policy is in log mode, so the content still reached the model provider.",
			found.PolicyName, plural(found.Users, "user"), gateway.WindowDays)
		payload, _ := json.Marshal(map[string]any{"policyID": found.PolicyID, "action": "redact"})
		finding.Treatment = &types.RiskTreatment{
			Kind:        TreatmentGuardrail,
			Description: "Switch the policy from log to redact, so the matched text is replaced before the request leaves Obot.",
			Payload:     payload,
			Replay: &types.RiskReplay{
				WindowDays: gateway.WindowDays,
				Events:     found.Count,
				Users:      found.Users,
				Note:       "Requests the policy would have redacted.",
			},
		}
		findings = append(findings, finding)
	}
	return findings
}

func detectEgress(gateway GatewayObservation) []Finding {
	var (
		open     int
		evidence []types.RiskEvidence
	)
	for _, server := range gateway.HostedServers {
		if server.EgressRestricted {
			continue
		}
		open++
		if len(evidence) < maxEvidencePerFinding {
			evidence = append(evidence, types.RiskEvidence{Source: types.RiskSourceObot, Kind: "hosted-mcp-server", Ref: server.ID, Detail: fmt.Sprintf("%s (%s)", server.Name, server.Runtime)})
		}
	}
	if open == 0 {
		return nil
	}
	return []Finding{{
		Type:     TypeUnrestrictedEgress,
		Key:      "hosted",
		Segment:  types.RiskSegmentPlatform,
		Severity: types.RiskSeverityMedium,
		Title:    fmt.Sprintf("%s can reach any network destination", plural(open, "hosted MCP server")),
		Summary: fmt.Sprintf("%d of the %s Obot runs have no egress rule. A server that is tricked into sending data out has nothing stopping it.",
			open, plural(len(gateway.HostedServers), "MCP server")),
		Sources:  []types.RiskSourceKind{types.RiskSourceObot},
		Metrics:  map[string]int{"hostedServers": len(gateway.HostedServers), "unrestricted": open},
		Evidence: evidence,
		Treatment: &types.RiskTreatment{
			Kind:        TreatmentEgress,
			Description: "Set the domains each server needs, or deny all egress for servers that need none. Egress rules need a network policy provider and apply to servers Obot hosts.",
		},
		Frameworks: []string{frameworkToolMisuse, frameworkSensitiveInfo},
	}}
}

func detectRetention(gateway GatewayObservation) []Finding {
	retention := gateway.Retention
	if retention == nil || retention.ScheduledExports > 0 {
		return nil
	}
	// Zero means records are kept forever.
	shortest := 0
	for _, days := range []int{retention.MCPDays, retention.LLMDays} {
		if days > 0 && (shortest == 0 || days < shortest) {
			shortest = days
		}
	}
	if shortest == 0 || shortest >= minEvidenceRetentionDays {
		return nil
	}
	return []Finding{{
		Type:     TypeRecordsExpire,
		Key:      "audit-logs",
		Segment:  types.RiskSegmentPlatform,
		Severity: types.RiskSeverityMedium,
		Title:    fmt.Sprintf("Audit records are deleted after %d days and nothing exports them", shortest),
		Summary: fmt.Sprintf("MCP audit logs are kept %d days and LLM audit logs %d days, with no scheduled export. Evidence that a policy held, or what happened in an incident, is gone after that.",
			retention.MCPDays, retention.LLMDays),
		Sources:  []types.RiskSourceKind{types.RiskSourceObot},
		Metrics:  map[string]int{"mcpDays": retention.MCPDays, "llmDays": retention.LLMDays},
		Evidence: []types.RiskEvidence{{Source: types.RiskSourceObot, Kind: "server-configuration", Detail: "Audit log retention and scheduled exports."}},
		Treatment: &types.RiskTreatment{
			Kind:        TreatmentRetention,
			Description: fmt.Sprintf("Schedule an audit log export to your own storage, or raise retention to at least %d days.", minEvidenceRetentionDays),
		},
		Frameworks: []string{frameworkEventLogs, frameworkPostDeployment},
	}}
}

// serverIdentity describes an observed server by what it runs or where it points.
func serverIdentity(server MCPServerObservation) string {
	switch {
	case server.URL != "":
		return fmt.Sprintf("%q (%s)", server.Name, server.URL)
	case server.Command != "":
		return fmt.Sprintf("%q (%s)", server.Name, strings.TrimSpace(server.Command+" "+strings.Join(server.Args, " ")))
	default:
		return fmt.Sprintf("%q", server.Name)
	}
}

// plural formats a count with its noun, so 1 device and 3 devices both read correctly.
func plural(n int, noun string) string {
	switch {
	case n == 1:
		return "1 " + noun
	case noun == "person":
		return fmt.Sprintf("%d people", n)
	default:
		return fmt.Sprintf("%d %ss", n, noun)
	}
}
