// Package aiusage attributes the model calls Obot makes itself, such as Obot AI's answers
// and the Supervisor's decisions, to the user who triggered them and the feature that made
// them, so they are counted in usage and spend and appear in the LLM audit log.
package aiusage

import (
	"context"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
)

const (
	// SystemUserID is who background work, which no user triggered, is counted against.
	SystemUserID = "obot"

	// Features.
	FeatureObotAI              = "obot-ai"
	FeatureSupervisorJudge     = "supervisor-judge"
	FeatureSupervisorTriage    = "supervisor-triage"
	FeatureSupervisorNarrative = "supervisor-commentary"
	FeatureSupervisorBriefing  = "supervisor-briefing"
	FeatureSupervisorAsk       = "supervisor-ask"
	FeaturePolicyDrafting      = "policy-drafting"
	FeatureToolLabels          = "tool-labels"
	FeatureContentTagger       = "content-tagger"
	FeatureMessagePolicy       = "message-policy"
)

type (
	featureKey struct{}
	userKey    struct{}
)

// Call is one model call Obot made.
type Call struct {
	Feature string
	// UserID is the user who triggered the call, or SystemUserID.
	UserID string
	// Model is the gateway model id the call was sent to, and ModelResource the Model it
	// resolved from, when known, for its prices.
	Model         string
	ModelResource *v1.Model
	Usage         *schemas.BifrostLLMUsage
	Duration      time.Duration
	Err           error
}

// Recorder records model calls.
type Recorder interface {
	Record(ctx context.Context, call Call)
}

// WithFeature names the feature the context's model calls are made for. An outer feature
// wins, so a call made on behalf of another feature, such as Obot AI asking the Supervisor,
// keeps the outer one.
func WithFeature(ctx context.Context, feature string) context.Context {
	if Feature(ctx) != "" {
		return ctx
	}
	return context.WithValue(ctx, featureKey{}, feature)
}

// WithUser names the user who triggered the context's model calls.
func WithUser(ctx context.Context, userID string) context.Context {
	if userID == "" {
		return ctx
	}
	return context.WithValue(ctx, userKey{}, userID)
}

// Feature is the context's feature, or "".
func Feature(ctx context.Context) string {
	f, _ := ctx.Value(featureKey{}).(string)
	return f
}

// User is the context's user, or SystemUserID.
func User(ctx context.Context) string {
	if u, _ := ctx.Value(userKey{}).(string); u != "" {
		return u
	}
	return SystemUserID
}
