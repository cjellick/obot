package riskregister

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
)

func TestPackageOf(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    ServerPackage
		wantOK  bool
	}{
		{
			name:    "npx with yes flag and scoped version",
			command: "npx",
			args:    []string{"-y", "@Modelcontextprotocol/server-github@1.2.3"},
			want:    ServerPackage{Source: types.AllowlistServerPackageSourceNPM, Name: "@modelcontextprotocol/server-github"},
			wantOK:  true,
		},
		{
			name:    "windows npx path",
			command: `C:\Program Files\nodejs\npx.cmd`,
			args:    []string{"mcp-remote", "https://example.com/mcp"},
			want:    ServerPackage{Source: types.AllowlistServerPackageSourceNPM, Name: "mcp-remote"},
			wantOK:  true,
		},
		{
			name:    "pnpm dlx",
			command: "pnpm",
			args:    []string{"dlx", "some-server"},
			want:    ServerPackage{Source: types.AllowlistServerPackageSourceNPM, Name: "some-server"},
			wantOK:  true,
		},
		{
			name:    "uvx with from and pinned version",
			command: "/usr/local/bin/uvx",
			args:    []string{"--from", "Mcp_Server.Git==2.0", "mcp-server-git"},
			want:    ServerPackage{Source: types.AllowlistServerPackageSourcePyPI, Name: "mcp-server-git"},
			wantOK:  true,
		},
		{
			name:    "uv tool run",
			command: "uv",
			args:    []string{"tool", "run", "awslabs.core-mcp-server@latest"},
			want:    ServerPackage{Source: types.AllowlistServerPackageSourcePyPI, Name: "awslabs-core-mcp-server"},
			wantOK:  true,
		},
		{
			name:    "bare executable",
			command: "/opt/tools/my-server",
			args:    []string{"--stdio"},
		},
		{
			name:    "pnpm without dlx",
			command: "pnpm",
			args:    []string{"run", "start"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := PackageOf(tt.command, tt.args)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("PackageOf() = %+v, %v; want %+v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestApprovedServersMatch(t *testing.T) {
	approved := ApprovedServers{
		ObotURL:   "https://obot.example.com",
		URLs:      []string{"https://mcp.linear.app/mcp"},
		Hostnames: []string{"api.githubcopilot.com"},
		Packages:  []ServerPackage{{Source: types.AllowlistServerPackageSourceNPM, Name: "@Modelcontextprotocol/server-github"}},
	}
	tests := []struct {
		name   string
		server MCPServerObservation
		want   bool
	}{
		{
			name:   "obot hosted",
			server: MCPServerObservation{URL: "https://obot.example.com/mcp-connect/abc"},
			want:   true,
		},
		{
			name:   "approved url with deeper path",
			server: MCPServerObservation{URL: "https://mcp.linear.app:443/mcp/sse"},
			want:   true,
		},
		{
			name:   "same host but different path",
			server: MCPServerObservation{URL: "https://mcp.linear.app/mcpx"},
		},
		{
			name:   "approved hostname",
			server: MCPServerObservation{URL: "https://API.githubcopilot.com/mcp/"},
			want:   true,
		},
		{
			name:   "approved package in another spelling",
			server: MCPServerObservation{Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}},
			want:   true,
		},
		{
			name:   "unknown package",
			server: MCPServerObservation{Command: "npx", Args: []string{"-y", "totally-new-server"}},
		},
		{
			name:   "unknown url",
			server: MCPServerObservation{URL: "http://mcp.linear.app/mcp"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _ := approved.Match(tt.server); got != tt.want {
				t.Fatalf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchApp(t *testing.T) {
	tests := []struct {
		name    string
		label   string
		want    string
		segment types.RiskSegment
	}{
		{
			name:    "copilot for developers",
			label:   "GitHub Copilot Business",
			want:    "GitHub Copilot",
			segment: types.RiskSegmentCodingAgent,
		},
		{
			name:    "chat assistant",
			label:   "ChatGPT Enterprise",
			want:    "ChatGPT",
			segment: types.RiskSegmentBusinessUser,
		},
		{
			name:    "api console",
			label:   "Anthropic Console",
			want:    "Claude Console",
			segment: types.RiskSegmentAPIWorkload,
		},
		{
			name:  "word boundary",
			label: "Precursor Analytics",
		},
		{
			name:  "unrelated",
			label: "Salesforce",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchApp(tt.label)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("MatchApp(%q) = %+v, want nil", tt.label, got)
				}
				return
			}
			if got == nil || got.Name != tt.want || got.Segment != tt.segment {
				t.Fatalf("MatchApp(%q) = %+v, want %s in %s", tt.label, got, tt.want, tt.segment)
			}
		})
	}
}

// TestDetectDayZero runs the checks over a snapshot with no gateway traffic, the way a new
// install looks: device scans, identity provider apps, vendor activity and provider keys.
func TestDetectDayZero(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	snapshot := Snapshot{
		CollectedAt: now,
		WindowDays:  DefaultWindowDays,
		Devices:     14,
		Clients: []ClientObservation{
			{Source: types.RiskSourceSentry, Name: "claude-code", Devices: 12, Users: 12},
			{Source: types.RiskSourceSentry, Name: "opencode", Devices: 6, Users: 6},
		},
		Approved: ApprovedServers{
			ObotURL: "https://obot.example.com",
			URLs:    []string{"https://mcp.linear.app/mcp"},
		},
		MCPServers: []MCPServerObservation{
			{Source: types.RiskSourceSentry, Key: "hash-approved", Name: "linear", Transport: "http", URL: "https://mcp.linear.app/mcp", Devices: 9, Users: 9},
			{Source: types.RiskSourceSentry, Key: "hash-npx", Name: "db", Transport: "stdio", Command: "npx", Args: []string{"-y", "some-db-mcp@0.3.1"}, Devices: 7, Users: 7, Calls: 41, CallUsers: 5},
			{Source: types.RiskSourceSentry, Key: "hash-bare", Name: "local", Transport: "stdio", Command: "/opt/bin/local-mcp", Devices: 1, Users: 1},
			{Source: types.RiskSourceRepositories, Key: "acme/app:.mcp.json:scraper", Name: "scraper", Transport: "http", URL: "https://scraper.example.net/mcp", Location: "acme/app (.mcp.json)"},
		},
		Apps: []AppObservation{
			{Source: types.RiskSourceOkta, ID: "0oa1", Label: "ChatGPT Enterprise", Active: true, AssignedUsers: 40, Match: MatchApp("ChatGPT Enterprise")},
			{Source: types.RiskSourceOkta, ID: "0oa2", Label: "Salesforce", Active: true, AssignedUsers: 300},
			{Source: types.RiskSourceOkta, ID: "0oa3", Label: "Cursor", Active: false, AssignedUsers: 5, Match: MatchApp("Cursor")},
		},
		Activity: []ActivityObservation{
			{Source: types.RiskSourceClaudeCompliance, Product: "Claude Code", Segment: types.RiskSegmentCodingAgent, Users: []string{"a@example.com", "b@example.com"}, Events: 9, LastSeen: now},
		},
		Connectors: []ConnectorObservation{
			{Source: types.RiskSourceClaudeCompliance, Name: "Linear", URL: "https://mcp.linear.app/mcp", Users: 3, Events: 3},
			{Source: types.RiskSourceClaudeCompliance, Name: "Scraper", URL: "https://scraper.example.net/mcp", Users: 6, Events: 8},
		},
		APIKeys: []APIKeyObservation{
			{Source: types.RiskSourceAnthropicAdmin, Provider: "Anthropic", ID: "apikey_old", Name: "ci", Active: true, CreatedAt: now.AddDate(0, 0, -200)},
			{Source: types.RiskSourceAnthropicAdmin, Provider: "Anthropic", ID: "apikey_new", Name: "dev", Active: true, CreatedAt: now.AddDate(0, 0, -3)},
			{Source: types.RiskSourceAnthropicAdmin, Provider: "Anthropic", ID: "apikey_off", Name: "gone", Active: false, CreatedAt: now.AddDate(-2, 0, 0)},
		},
		RepoSettings: []RepoSettingObservation{
			{Source: types.RiskSourceRepositories, Repository: "acme/app", File: ".claude/settings.json", Setting: RepoSettingBypassPermissions, Detail: "Permission prompts are turned off."},
		},
	}

	byFingerprint := map[string]Finding{}
	for _, finding := range Detect(snapshot) {
		byFingerprint[finding.Fingerprint()] = finding
	}

	want := map[string]types.RiskSeverity{
		TypeUnapprovedMCPServer + ":sentry:hash-npx":                                              types.RiskSeverityHigh,
		TypeUnapprovedMCPServer + ":sentry:hash-bare":                                             types.RiskSeverityMedium,
		TypeUnapprovedMCPServer + ":repositories:acme/app:.mcp.json:scraper":                      types.RiskSeverityMedium,
		TypeClientNotEnforced + ":opencode":                                                       types.RiskSeverityMedium,
		TypeAppOutsideGateway + ":okta:0oa1":                                                      types.RiskSeverityMedium,
		TypeUseOutsideGateway + ":claude-compliance:claude code":                                  types.RiskSeverityLow,
		TypeConnectorOutside + ":claude-compliance:scraper|https://scraper.example.net/mcp":       types.RiskSeverityHigh,
		TypeLongLivedProviderKeys + ":anthropic":                                                  types.RiskSeverityMedium,
		TypeRepoAgentSettings + ":acme/app|.claude/settings.json|" + RepoSettingBypassPermissions: types.RiskSeverityHigh,
	}
	for fingerprint, severity := range want {
		finding, ok := byFingerprint[fingerprint]
		if !ok {
			t.Errorf("missing finding %s", fingerprint)
			continue
		}
		if finding.Severity != severity {
			t.Errorf("%s severity = %s, want %s", fingerprint, finding.Severity, severity)
		}
		if finding.Treatment == nil || len(finding.Evidence) == 0 || finding.Title == "" || finding.Summary == "" {
			t.Errorf("%s is missing a treatment, evidence, title or summary: %+v", fingerprint, finding)
		}
	}
	if len(byFingerprint) != len(want) {
		t.Errorf("got %d findings, want %d: %v", len(byFingerprint), len(want), keys(byFingerprint))
	}

	// The npx server gets a package allow rule and a replay of the calls devices reported.
	npx := byFingerprint[TypeUnapprovedMCPServer+":sentry:hash-npx"].Treatment
	var rule types.AllowlistServer
	if err := json.Unmarshal(npx.Payload, &rule); err != nil || rule.Package == nil || rule.Package.Name != "some-db-mcp" {
		t.Errorf("npx allow rule = %s (%v), want package some-db-mcp", npx.Payload, err)
	}
	if npx.Replay == nil || npx.Replay.Events != 41 || npx.Replay.Users != 5 {
		t.Errorf("npx replay = %+v, want 41 events from 5 users", npx.Replay)
	}
	// A bare command can't be expressed as an allow rule.
	if bare := byFingerprint[TypeUnapprovedMCPServer+":sentry:hash-bare"].Treatment; len(bare.Payload) != 0 {
		t.Errorf("bare command got an allow rule: %s", bare.Payload)
	}
}

func TestDetectGateway(t *testing.T) {
	tests := []struct {
		name    string
		gateway GatewayObservation
		want    []string
	}{
		{
			name: "no traffic means no guardrail findings",
			gateway: GatewayObservation{
				WindowDays: 30,
				Retention:  &RetentionObservation{MCPDays: 0, LLMDays: 0},
			},
		},
		{
			name: "traffic with no guardrails",
			gateway: GatewayObservation{
				WindowDays: 30,
				Guardrails: GuardrailObservation{Requests: 1200},
			},
			want: []string{TypeSecretsNotChecked + ":llm-gateway", TypePersonalNotChecked + ":llm-gateway"},
		},
		{
			name: "logged secrets become their own entry",
			gateway: GatewayObservation{
				WindowDays: 30,
				Guardrails: GuardrailObservation{
					Requests:     1200,
					AppliedTypes: []string{"secrets", "regex"},
					Findings: []GuardrailFindingObservation{
						{Category: "secret", PolicyID: "p1", PolicyName: "Secrets", Outcome: "logged", Count: 14, Users: 4},
						{Category: "secret", PolicyID: "p2", PolicyName: "Secrets strict", Outcome: "redacted", Count: 3, Users: 1},
						{Category: "EMAIL", PolicyID: "p3", PolicyName: "PII", Outcome: "logged", Count: 30, Users: 9},
					},
				},
			},
			want: []string{TypeSecretsInTraffic + ":p1|secret", TypeSensitiveInTraffic + ":p3|EMAIL"},
		},
		{
			name: "wide unused grants, open egress and short retention",
			gateway: GatewayObservation{
				WindowDays: 30,
				ToolGrants: []ToolGrantObservation{
					{VMCPID: "vmcp1", VMCPName: "Engineering", Profile: "default", Granted: 140, Used: 11, Unused: []string{"delete_repo"}},
					{VMCPID: "vmcp2", VMCPName: "Small", Profile: "default", Granted: 6, Used: 4},
				},
				ModelGrants: []ModelGrantObservation{
					{PolicyID: "map1", PolicyName: "Everyone", Granted: 20, Used: 3},
				},
				HostedServers: []HostedServerObservation{
					{ID: "ms1", Name: "github", Runtime: "npx"},
					{ID: "ms2", Name: "slack", Runtime: "uvx", EgressRestricted: true},
				},
				Retention: &RetentionObservation{MCPDays: 90, LLMDays: 90},
			},
			want: []string{
				TypeUnusedToolGrants + ":vmcp1|default",
				TypeUnusedModelGrants + ":map1",
				TypeUnrestrictedEgress + ":hosted",
				TypeRecordsExpire + ":audit-logs",
			},
		},
		{
			name: "a scheduled export covers short retention",
			gateway: GatewayObservation{
				WindowDays: 30,
				Retention:  &RetentionObservation{MCPDays: 90, LLMDays: 90, ScheduledExports: 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := map[string]bool{}
			for _, finding := range Detect(Snapshot{WindowDays: 30, Gateway: &tt.gateway}) {
				got[finding.Fingerprint()] = true
			}
			for _, fingerprint := range tt.want {
				if !got[fingerprint] {
					t.Errorf("missing finding %s", fingerprint)
				}
			}
			if len(got) != len(tt.want) {
				t.Errorf("got %d findings, want %d: %v", len(got), len(tt.want), keys(got))
			}
		})
	}
}

func TestBuildUsageProfile(t *testing.T) {
	snapshot := Snapshot{
		EnforcementEnabled: true,
		Clients: []ClientObservation{
			{Source: types.RiskSourceSentry, Name: "claude-code", Devices: 12, Users: 11},
			{Source: types.RiskSourceSentry, Name: "vscode", Devices: 9, Users: 9},
			{Source: types.RiskSourceSentry, Name: "opencode", Devices: 2, Users: 2},
		},
		Apps: []AppObservation{
			{Source: types.RiskSourceOkta, ID: "1", Label: "ChatGPT Enterprise", Active: true, AssignedUsers: 40, Match: MatchApp("ChatGPT Enterprise")},
		},
		Activity: []ActivityObservation{
			{Source: types.RiskSourceClaudeCompliance, Product: "Claude Code", Segment: types.RiskSegmentCodingAgent, Users: []string{"a", "b", "c"}},
			{Source: types.RiskSourceClaudeCompliance, Product: "Claude chat", Segment: types.RiskSegmentBusinessUser, Users: []string{"a", "b"}},
		},
		APIKeys: []APIKeyObservation{
			{Source: types.RiskSourceAnthropicAdmin, Provider: "Anthropic", ID: "k1", Active: true},
			{Source: types.RiskSourceAnthropicAdmin, Provider: "Anthropic", ID: "k2", Active: true},
			{Source: types.RiskSourceAnthropicAdmin, Provider: "Anthropic", ID: "k3"},
		},
	}
	profile := BuildUsageProfile(snapshot, map[types.RiskSegment]int{types.RiskSegmentCodingAgent: 4})

	if len(profile.Segments) != 3 {
		t.Fatalf("got %d segments, want 3: %+v", len(profile.Segments), profile.Segments)
	}
	coding := profile.Segments[0]
	if coding.Segment != types.RiskSegmentCodingAgent || coding.Users != 11 || coding.Devices != 12 || coding.OpenRisks != 4 {
		t.Errorf("coding segment = %+v", coding)
	}
	if coding.ToolsByControl[types.RiskControlEnforced] != 1 || coding.ToolsByControl[types.RiskControlAudited] != 1 || coding.ToolsByControl[types.RiskControlObserved] != 1 {
		t.Errorf("coding control counts = %+v", coding.ToolsByControl)
	}
	// Claude Code is seen by Sentry and by the vendor's activity feed, and listed once.
	if tool := coding.Tools[0]; tool.Name != "Claude Code" || len(tool.Sources) != 2 || tool.Control != types.RiskControlEnforced {
		t.Errorf("first coding tool = %+v", tool)
	}
	if business := profile.Segments[1]; business.Segment != types.RiskSegmentBusinessUser || business.Users != 40 || len(business.Tools) != 2 {
		t.Errorf("business segment = %+v", business)
	}
	if api := profile.Segments[2]; api.Segment != types.RiskSegmentAPIWorkload || len(api.Tools) != 1 || api.Tools[0].Keys != 2 {
		t.Errorf("api segment = %+v", api)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
