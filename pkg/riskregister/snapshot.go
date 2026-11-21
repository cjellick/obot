// Package riskregister turns what Obot and connected sources observe about AI use into
// entries in a risk register. This file defines what collectors report. The package does no
// I/O itself, so everything in it can be tested with plain data.
package riskregister

import (
	"time"

	"github.com/obot-platform/obot/apiclient/types"
)

const (
	// DefaultWindowDays is how far back collectors look at activity.
	DefaultWindowDays = 30

	// RepoSettingBypassPermissions is a repository setting that turns off an agent's
	// permission prompts.
	RepoSettingBypassPermissions = "bypass-permissions"
	// RepoSettingAllProjectMCP is a repository setting that starts every MCP server the
	// repository defines without asking.
	RepoSettingAllProjectMCP = "enable-all-project-mcp-servers"
	// RepoSettingBroadAllow is a repository allow rule that covers any shell command.
	RepoSettingBroadAllow = "broad-allow-rule"
)

// Snapshot is everything the sources reported in one collection run.
type Snapshot struct {
	CollectedAt time.Time
	// WindowDays is how far back the activity in this snapshot goes.
	WindowDays int
	Sources    []types.RiskSourceStatus

	// Device inventory, from Obot Sentry.
	Devices int
	Clients []ClientObservation
	// EnforcementEnabled is true when at least one device configuration enforces tool calls.
	EnforcementEnabled bool

	// MCP servers seen on devices, in repositories, or added in a model vendor's own apps.
	MCPServers []MCPServerObservation

	// What Obot already treats as approved, to compare MCP servers against.
	Approved ApprovedServers

	// AI applications assigned in the identity provider.
	Apps []AppObservation

	// Activity in a model vendor's own products, such as Claude chat or Claude Code.
	Activity []ActivityObservation

	// Connectors people attached inside a model vendor's own apps.
	Connectors []ConnectorObservation

	// API keys issued by a model provider.
	APIKeys []APIKeyObservation

	// Agent settings checked into repositories.
	RepoSettings []RepoSettingObservation

	// What Obot's own gateways know.
	Gateway *GatewayObservation
}

// ClientObservation is an AI client installed on devices.
type ClientObservation struct {
	Source  types.RiskSourceKind
	Name    string
	Devices int
	Users   int
}

// MCPServerObservation is an MCP server configured somewhere Obot does not host it.
type MCPServerObservation struct {
	Source types.RiskSourceKind
	// Key identifies the server within its source: a config hash for devices, a repository
	// and file for repositories.
	Key       string
	Name      string
	Transport string
	URL       string
	Command   string
	Args      []string
	Devices   int
	Users     int
	// Location says where a repository-defined server was found.
	Location string
	// Calls is how many tool calls devices reported to this server in the replay window.
	Calls     int
	CallUsers int
}

// AppObservation is an application in the identity provider.
type AppObservation struct {
	Source        types.RiskSourceKind
	ID            string
	Label         string
	Active        bool
	AssignedUsers int
	// AI classification. Match is empty when the app wasn't recognized as an AI tool.
	Match *AIApp
}

// ActivityObservation is use of one vendor product over the collection window.
type ActivityObservation struct {
	Source  types.RiskSourceKind
	Product string
	Segment types.RiskSegment
	// Users holds lowercased account identifiers, used only to compare against gateway
	// users. They are counted, never stored.
	Users    []string
	Events   int
	LastSeen time.Time
}

// ConnectorObservation is a connector or MCP server attached inside a vendor's own app.
type ConnectorObservation struct {
	Source types.RiskSourceKind
	Name   string
	URL    string
	Users  int
	Events int
}

// APIKeyObservation is an API key issued by a model provider.
type APIKeyObservation struct {
	Source    types.RiskSourceKind
	Provider  string
	ID        string
	Name      string
	Active    bool
	Scope     string
	CreatedAt time.Time
}

// RepoSettingObservation is an agent setting in a repository that widens what an agent may do
// without asking.
type RepoSettingObservation struct {
	Source     types.RiskSourceKind
	Repository string
	File       string
	Setting    string
	Detail     string
}

// GatewayObservation is what Obot's own configuration and audit logs show.
type GatewayObservation struct {
	WindowDays int
	// LLMUsers and MCPUsers hold lowercased emails of people seen on each gateway in the
	// window. They are counted, never stored.
	LLMUsers []string
	MCPUsers []string

	ToolGrants  []ToolGrantObservation
	ModelGrants []ModelGrantObservation

	// Guardrail coverage and findings on model traffic.
	Guardrails GuardrailObservation

	HostedServers []HostedServerObservation

	// Retention is nil when it couldn't be read.
	Retention *RetentionObservation
}

// ToolGrantObservation compares the tools a shared vMCP profile grants with the tools called.
type ToolGrantObservation struct {
	VMCPID   string
	VMCPName string
	Profile  string
	Granted  int
	Used     int
	// Unused is a sample of granted tool names with no calls in the window.
	Unused []string
}

// ModelGrantObservation compares the models a profile grants with the models requested.
type ModelGrantObservation struct {
	PolicyID   string
	PolicyName string
	Granted    int
	Used       int
	Unused     []string
}

// GuardrailObservation is guardrail coverage of model traffic.
type GuardrailObservation struct {
	// Requests is the number of model requests in the window. Zero means the LLM gateway
	// isn't carrying traffic, so missing guardrails aren't reported.
	Requests int
	// AppliedTypes holds the guardrail types that at least one enabled policy applies.
	AppliedTypes []string
	Findings     []GuardrailFindingObservation
}

// GuardrailFindingObservation counts findings of one category and outcome.
type GuardrailFindingObservation struct {
	Category   string
	PolicyID   string
	PolicyName string
	Outcome    string
	Count      int
	Users      int
}

// HostedServerObservation is an MCP server Obot hosts.
type HostedServerObservation struct {
	ID               string
	Name             string
	Runtime          string
	EgressRestricted bool
}

// RetentionObservation is how long audit records are kept.
type RetentionObservation struct {
	MCPDays int
	LLMDays int
	// ScheduledExports is the number of enabled scheduled exports.
	ScheduledExports int
}
