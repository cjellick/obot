package types

import "time"

// LLM gateway budget binding subject types.
const (
	LLMGatewayBindingSubjectUser     = "user"
	LLMGatewayBindingSubjectGroup    = "group"
	LLMGatewayBindingSubjectEveryone = "everyone"
)

// LLMGatewayBudgetBinding assigns an Obot user, group, or everyone to a Bifrost virtual
// key. When a caller matches a binding, the gateway forwards their requests under that
// virtual key, so the key's budgets, rate limits, and provider/model restrictions apply.
type LLMGatewayBudgetBinding struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	CreatedAt    time.Time `json:"created"`
	UpdatedAt    time.Time `json:"updated"`
	VirtualKeyID string    `gorm:"type:text;index;not null" json:"virtualKeyID"`
	SubjectType  string    `gorm:"type:text;not null;index:idx_llm_gateway_binding_subject,priority:1" json:"subjectType"`
	SubjectID    string    `gorm:"type:text;index:idx_llm_gateway_binding_subject,priority:2" json:"subjectID,omitempty"`
}

func (LLMGatewayBudgetBinding) TableName() string {
	return "llm_gateway_budget_bindings"
}
