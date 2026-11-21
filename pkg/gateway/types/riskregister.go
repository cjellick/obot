//nolint:revive
package types

import (
	"time"

	types2 "github.com/obot-platform/obot/apiclient/types"
)

// Risk is one entry in the AI risk register. Fingerprint identifies the same condition from
// one collection run to the next, so a person's decision stays attached to it.
type Risk struct {
	ID          uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Fingerprint string    `json:"fingerprint" gorm:"uniqueIndex;not null"`
	Type        string    `json:"type" gorm:"index"`
	Segment     string    `json:"segment" gorm:"index"`
	Severity    string    `json:"severity" gorm:"index"`
	Status      string    `json:"status" gorm:"index"`
	Title       string    `json:"title" gorm:"type:text"`
	Summary     string    `json:"summary" gorm:"type:text"`
	// Analysis is a model-written note. It is never used as evidence.
	Analysis    string                `json:"analysis" gorm:"type:text"`
	Sources     []string              `json:"sources" gorm:"serializer:json"`
	Evidence    []types2.RiskEvidence `json:"evidence" gorm:"serializer:json"`
	Metrics     map[string]int        `json:"metrics" gorm:"serializer:json"`
	Treatment   *types2.RiskTreatment `json:"treatment" gorm:"serializer:json"`
	Frameworks  []string              `json:"frameworks" gorm:"serializer:json"`
	Owner       string                `json:"owner"`
	FirstSeenAt time.Time             `json:"firstSeenAt"`
	LastSeenAt  time.Time             `json:"lastSeenAt"`
	// Observed is false once a collection run no longer finds the condition.
	Observed     bool                       `json:"observed"`
	TreatedAt    *time.Time                 `json:"treatedAt"`
	TreatmentRef string                     `json:"treatmentRef"`
	Proof        *types2.RiskEvidenceStatus `json:"proof" gorm:"serializer:json"`
}

// RiskDecision records what a person decided about a risk, so the register keeps who, when
// and why.
type RiskDecision struct {
	ID         uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt  time.Time `json:"createdAt"`
	RiskID     uint      `json:"riskID" gorm:"index;not null"`
	UserID     string    `json:"userID"`
	Username   string    `json:"username"`
	Action     string    `json:"action"`
	Note       string    `json:"note" gorm:"type:text"`
	FromStatus string    `json:"fromStatus"`
	ToStatus   string    `json:"toStatus"`
}

// RiskSource is the configuration of one place the register collects from. Its secrets live
// in the credential store.
type RiskSource struct {
	Kind       string                   `json:"kind" gorm:"primaryKey;type:text"`
	UpdatedAt  time.Time                `json:"updatedAt"`
	Enabled    bool                     `json:"enabled"`
	Config     map[string]string        `json:"config" gorm:"serializer:json"`
	LastStatus *types2.RiskSourceStatus `json:"lastStatus" gorm:"serializer:json"`
}

// RiskCollectionRun is one pass over the sources.
type RiskCollectionRun struct {
	ID         uint                      `json:"id" gorm:"primaryKey;autoIncrement"`
	StartedAt  time.Time                 `json:"startedAt" gorm:"index"`
	FinishedAt *time.Time                `json:"finishedAt"`
	Trigger    string                    `json:"trigger"`
	Sources    []types2.RiskSourceStatus `json:"sources" gorm:"serializer:json"`
	Profile    *types2.RiskUsageProfile  `json:"profile" gorm:"serializer:json"`
	Found      int                       `json:"found"`
	New        int                       `json:"new"`
	Error      string                    `json:"error" gorm:"type:text"`
	Analyzed   bool                      `json:"analyzed"`
}
