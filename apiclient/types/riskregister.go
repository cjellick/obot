package types

import (
	"encoding/json"
)

const (
	// RiskSegmentCodingAgent covers AI coding tools that act on a developer's machine or
	// repositories, such as Claude Code, Codex and Cursor.
	RiskSegmentCodingAgent RiskSegment = "coding-agent"
	// RiskSegmentBusinessUser covers chat and office assistants used for knowledge work.
	RiskSegmentBusinessUser RiskSegment = "business-user"
	// RiskSegmentAPIWorkload covers programs that call models directly with an API key.
	RiskSegmentAPIWorkload RiskSegment = "api-workload"
	// RiskSegmentPlatform covers risks in how Obot itself is configured, which apply to every
	// kind of use that goes through it.
	RiskSegmentPlatform RiskSegment = "platform"
	// RiskSegmentUnknown is used when a source can't tell which kind of use it saw.
	RiskSegmentUnknown RiskSegment = "unknown"

	RiskSeverityLow    RiskSeverity = "low"
	RiskSeverityMedium RiskSeverity = "medium"
	RiskSeverityHigh   RiskSeverity = "high"

	// RiskStatusOpen is a risk nobody has decided on yet.
	RiskStatusOpen RiskStatus = "open"
	// RiskStatusAccepted is a risk the organization has decided to live with.
	RiskStatusAccepted RiskStatus = "accepted"
	// RiskStatusTreated is a risk with a policy in place; its evidence is tracked.
	RiskStatusTreated RiskStatus = "treated"
	// RiskStatusDismissed is a finding judged not to be a risk.
	RiskStatusDismissed RiskStatus = "dismissed"

	RiskDecisionAccept  RiskDecisionAction = "accept"
	RiskDecisionTreat   RiskDecisionAction = "treat"
	RiskDecisionDismiss RiskDecisionAction = "dismiss"
	RiskDecisionReopen  RiskDecisionAction = "reopen"
	RiskDecisionAssign  RiskDecisionAction = "assign"
	RiskDecisionComment RiskDecisionAction = "comment"

	RiskSourceSentry           RiskSourceKind = "sentry"
	RiskSourceObot             RiskSourceKind = "obot"
	RiskSourceOkta             RiskSourceKind = "okta"
	RiskSourceClaudeCompliance RiskSourceKind = "claude-compliance"
	RiskSourceAnthropicAdmin   RiskSourceKind = "anthropic-admin"
	RiskSourceRepositories     RiskSourceKind = "repositories"

	// RiskControlEnforced means Obot can block this use today.
	RiskControlEnforced RiskControl = "enforced"
	// RiskControlAudited means Obot records this use but can't block it.
	RiskControlAudited RiskControl = "audited"
	// RiskControlObserved means Obot only knows this use exists.
	RiskControlObserved RiskControl = "observed"
)

type (
	RiskSegment        string
	RiskSeverity       string
	RiskStatus         string
	RiskDecisionAction string
	RiskSourceKind     string
	RiskControl        string
)

// RiskEvidence points at one record behind a risk. It holds identifiers and counts, never
// message content.
type RiskEvidence struct {
	Source RiskSourceKind `json:"source"`
	// Kind names the record type, such as "device-mcp-server" or "guardrail-finding".
	Kind string `json:"kind"`
	// Ref identifies the record in its source, such as a config hash or an app ID.
	Ref    string `json:"ref,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// RiskTreatment is a proposed policy for a risk.
type RiskTreatment struct {
	// Kind names the Obot policy type that would treat the risk, or "none" when Obot has no
	// policy for it.
	Kind        string `json:"kind"`
	Description string `json:"description"`
	// Payload is the concrete change in the target policy's own JSON shape, when one can be
	// drafted.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Replay says what the policy would have affected in past activity.
	Replay *RiskReplay `json:"replay,omitempty"`
}

// RiskReplay is what a proposed policy would have caught over a past window.
type RiskReplay struct {
	WindowDays int    `json:"windowDays"`
	Events     int    `json:"events"`
	Users      int    `json:"users,omitempty"`
	Note       string `json:"note,omitempty"`
}

// RiskEvidenceStatus is the latest check of a treated risk.
type RiskEvidenceStatus struct {
	CheckedAt Time `json:"checkedAt"`
	// StillObserved is true when the latest collection still found the condition.
	StillObserved bool           `json:"stillObserved"`
	Metrics       map[string]int `json:"metrics,omitempty"`
	Note          string         `json:"note,omitempty"`
}

// Risk is one entry in the AI risk register.
type Risk struct {
	ID          uint           `json:"id"`
	Created     Time           `json:"created"`
	Type        string         `json:"type"`
	Segment     RiskSegment    `json:"segment"`
	Severity    RiskSeverity   `json:"severity"`
	Status      RiskStatus     `json:"status"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary"`
	Analysis    string         `json:"analysis,omitempty"`
	Sources     []string       `json:"sources,omitempty"`
	Evidence    []RiskEvidence `json:"evidence,omitempty"`
	Metrics     map[string]int `json:"metrics,omitempty"`
	Treatment   *RiskTreatment `json:"treatment,omitempty"`
	Frameworks  []string       `json:"frameworks,omitempty"`
	Owner       string         `json:"owner,omitempty"`
	FirstSeenAt Time           `json:"firstSeenAt"`
	LastSeenAt  Time           `json:"lastSeenAt"`
	// Observed is false when the latest collection no longer found the condition.
	Observed     bool                `json:"observed"`
	TreatedAt    *Time               `json:"treatedAt,omitempty"`
	TreatmentRef string              `json:"treatmentRef,omitempty"`
	Proof        *RiskEvidenceStatus `json:"proof,omitempty"`
	Decisions    []RiskDecision      `json:"decisions,omitempty"`
}

type RiskList List[Risk]

// RiskDecision records what a person decided about a risk.
type RiskDecision struct {
	ID       uint               `json:"id"`
	Created  Time               `json:"created"`
	UserID   string             `json:"userID"`
	Username string             `json:"username,omitempty"`
	Action   RiskDecisionAction `json:"action"`
	Note     string             `json:"note,omitempty"`
	From     RiskStatus         `json:"from,omitempty"`
	To       RiskStatus         `json:"to,omitempty"`
}

// RiskDecisionRequest is the body for deciding on a risk.
type RiskDecisionRequest struct {
	Action RiskDecisionAction `json:"action"`
	Note   string             `json:"note,omitempty"`
	// Owner is set by the assign action.
	Owner string `json:"owner,omitempty"`
	// TreatmentRef names the policy that treats the risk, for the treat action.
	TreatmentRef string `json:"treatmentRef,omitempty"`
}

// RiskSource is one place the register collects from.
type RiskSource struct {
	Kind    RiskSourceKind    `json:"kind"`
	Enabled bool              `json:"enabled"`
	Config  map[string]string `json:"config,omitempty"`
	// ConfiguredSecrets names the secrets that are stored. Their values are never returned.
	ConfiguredSecrets []string          `json:"configuredSecrets,omitempty"`
	Status            *RiskSourceStatus `json:"status,omitempty"`
}

type RiskSourceList List[RiskSource]

// RiskSourceRequest is the body for configuring a source. An empty secret keeps the stored
// value.
type RiskSourceRequest struct {
	Enabled bool              `json:"enabled"`
	Config  map[string]string `json:"config,omitempty"`
	Secrets map[string]string `json:"secrets,omitempty"`
}

// RiskSourceStatus is how a source did in a collection run.
type RiskSourceStatus struct {
	Kind    RiskSourceKind `json:"kind"`
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Counts  map[string]int `json:"counts,omitempty"`
	Skipped string         `json:"skipped,omitempty"`
}

// RiskUsageTool is one AI tool seen in a segment.
type RiskUsageTool struct {
	Name    string           `json:"name"`
	Vendor  string           `json:"vendor,omitempty"`
	Sources []RiskSourceKind `json:"sources"`
	Users   int              `json:"users,omitempty"`
	Devices int              `json:"devices,omitempty"`
	// Keys is the number of active API keys, for API workloads.
	Keys    int         `json:"keys,omitempty"`
	Control RiskControl `json:"control"`
}

// RiskUsageSegment summarizes one kind of AI use.
type RiskUsageSegment struct {
	Segment RiskSegment     `json:"segment"`
	Tools   []RiskUsageTool `json:"tools"`
	// Users is the largest per-tool user count. Sources identify people differently, so
	// counts aren't added across tools.
	Users   int `json:"users"`
	Devices int `json:"devices,omitempty"`
	// ToolsByControl counts tools by how much control Obot has over them.
	ToolsByControl map[RiskControl]int `json:"toolsByControl"`
	OpenRisks      int                 `json:"openRisks"`
}

// RiskUsageProfile says who uses AI and how, by segment.
type RiskUsageProfile struct {
	Segments []RiskUsageSegment `json:"segments"`
}

// RiskCollectionRun is one pass over the sources.
type RiskCollectionRun struct {
	ID         uint               `json:"id"`
	StartedAt  Time               `json:"startedAt"`
	FinishedAt *Time              `json:"finishedAt,omitempty"`
	Trigger    string             `json:"trigger"`
	Sources    []RiskSourceStatus `json:"sources,omitempty"`
	Profile    *RiskUsageProfile  `json:"profile,omitempty"`
	Found      int                `json:"found"`
	New        int                `json:"new"`
	Error      string             `json:"error,omitempty"`
	// Analyzed is true when a model ranked and annotated the findings.
	Analyzed bool `json:"analyzed"`
}
