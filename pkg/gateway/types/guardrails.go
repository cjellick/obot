package types

import (
	"encoding/json"
	"time"
)

const (
	// Guardrail targets.
	GuardrailTargetLLM = "llm"
	GuardrailTargetMCP = "mcp"

	// Guardrail stages a rule applies to.
	GuardrailApplyInput  = "input"
	GuardrailApplyOutput = "output"
	GuardrailApplyBoth   = "both"

	// Guardrail actions.
	GuardrailActionBlock  = "block"
	GuardrailActionRedact = "redact"
	GuardrailActionLog    = "log"

	// Guardrail input scopes.
	GuardrailScopeAll    = "all"
	GuardrailScopeLatest = "latest"
)

// GuardrailRule decides which requests or tool calls are checked, by which profiles, and
// what happens when a profile finds a problem.
type GuardrailRule struct {
	ID          string    `gorm:"primaryKey;type:text" json:"id"`
	CreatedAt   time.Time `json:"created"`
	UpdatedAt   time.Time `json:"updated"`
	Name        string    `gorm:"type:text;not null" json:"name"`
	Description string    `gorm:"type:text" json:"description,omitempty"`
	Enabled     bool      `gorm:"not null" json:"enabled"`
	// Target is llm (model requests and responses) or mcp (tool call arguments and results).
	Target string `gorm:"type:text;not null" json:"target"`
	// ApplyTo is input, output or both.
	ApplyTo string `gorm:"type:text;not null" json:"applyTo"`
	// Action is block, redact or log.
	Action string `gorm:"type:text;not null" json:"action"`
	// Condition is an optional CEL expression that must be true for the rule to apply.
	Condition string `gorm:"type:text" json:"condition,omitempty"`
	// SamplingRate is the percentage (1-100) of matching requests the rule checks.
	SamplingRate int `gorm:"not null" json:"samplingRate"`
	// InputScope is all (every message in the request) or latest (only messages since the
	// model last responded). Redacting rules always check every message.
	InputScope string `gorm:"type:text" json:"inputScope,omitempty"`
	// FailClosed blocks the request when a profile can't be evaluated.
	FailClosed bool `gorm:"not null" json:"failClosed"`
	// BlockMessage replaces the default message returned when the rule blocks.
	BlockMessage string                 `gorm:"type:text" json:"blockMessage,omitempty"`
	ProfileIDs   []string               `gorm:"serializer:json" json:"profileIDs"`
	Subjects     []GuardrailRuleSubject `gorm:"serializer:json" json:"subjects"`
}

// GuardrailRuleSubject limits a rule to a user, a group or everyone.
type GuardrailRuleSubject struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

// GuardrailProfile configures one checker. Sensitive settings are kept in the encrypted
// credential store, not here.
type GuardrailProfile struct {
	ID          string          `gorm:"primaryKey;type:text" json:"id"`
	CreatedAt   time.Time       `json:"created"`
	UpdatedAt   time.Time       `json:"updated"`
	Name        string          `gorm:"type:text;not null" json:"name"`
	Description string          `gorm:"type:text" json:"description,omitempty"`
	Type        string          `gorm:"type:text;not null" json:"type"`
	Config      json.RawMessage `json:"config,omitempty"`
}

// GuardrailEvent records a guardrail finding or evaluation failure.
type GuardrailEvent struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	CreatedAt   time.Time `gorm:"index" json:"created"`
	RuleID      string    `gorm:"type:text;index" json:"ruleID"`
	RuleName    string    `gorm:"type:text" json:"ruleName"`
	ProfileID   string    `gorm:"type:text" json:"profileID,omitempty"`
	ProfileName string    `gorm:"type:text" json:"profileName,omitempty"`
	ProfileType string    `gorm:"type:text" json:"profileType,omitempty"`
	Target      string    `gorm:"type:text" json:"target"`
	Stage       string    `gorm:"type:text" json:"stage"`
	// Outcome is blocked, redacted, logged or error.
	Outcome  string `gorm:"type:text;index" json:"outcome"`
	UserID   string `gorm:"type:text;index" json:"userID,omitempty"`
	Model    string `gorm:"type:text" json:"model,omitempty"`
	Resource string `gorm:"type:text" json:"resource,omitempty"`
	// Findings describes what was found, without the offending content.
	Findings json.RawMessage `json:"findings,omitempty"`
	Error    string          `gorm:"type:text" json:"error,omitempty"`
}

func (GuardrailRule) TableName() string {
	return "guardrail_rules"
}

func (GuardrailProfile) TableName() string {
	return "guardrail_profiles"
}

func (GuardrailEvent) TableName() string {
	return "guardrail_events"
}
