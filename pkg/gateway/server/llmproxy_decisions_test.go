package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/authentication/user"
)

// decisionFixture stands in for a provider: it answers decision requests with a canned
// decision and records the request the gateway made.
type decisionFixture struct {
	request *schemas.BifrostDecisionRequest
}

func (*decisionFixture) GetName() string { return "decision-fixture" }
func (*decisionFixture) Cleanup() error  { return nil }
func (*decisionFixture) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error {
	return nil
}
func (*decisionFixture) PostLLMHook(_ *schemas.BifrostContext, r *schemas.BifrostResponse, e *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return r, e, nil
}
func (p *decisionFixture) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	if req.DecisionRequest == nil {
		return req, nil, nil
	}
	p.request = req.DecisionRequest
	return req, &schemas.LLMPluginShortCircuit{Response: &schemas.BifrostResponse{DecisionResponse: &schemas.BifrostDecisionResponse{
		ID:    "internal",
		Model: req.DecisionRequest.Model,
		Answers: []schemas.DecisionAnswer{{
			Type:        schemas.DecisionTypePredicate,
			Name:        schemas.Ptr("pii"),
			Probability: schemas.Ptr(0.9),
		}},
		Usage: &schemas.BifrostLLMUsage{PromptTokens: 100, CompletionTokens: 2, TotalTokens: 102},
	}}}, nil
}

func TestProxyDecisions(t *testing.T) {
	fixture := &decisionFixture{}
	proxy, req := directProxy(t, fixture)
	body := `{"model":"m1-test","input":"Jane Doe, jane@example.com","questions":[{"type":"predicate","name":"pii","instructions":"Does the input contain personal data?"}]}`
	req.Request = httptest.NewRequest(http.MethodPost, "/api/llm-proxy/v1/decisions", strings.NewReader(body))
	req.SetPathValue("path", "v1/decisions")
	w := httptest.NewRecorder()
	req.ResponseWriter = w
	require.NoError(t, proxy.proxy(req))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// The Obot model name is resolved to the gateway model it targets.
	require.NotNil(t, fixture.request)
	require.Equal(t, schemas.OpenAI, fixture.request.Provider)
	require.Equal(t, "test", fixture.request.Model)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotContains(t, resp, "extra_fields")
	require.Equal(t, float64(100), resp["usage"].(map[string]any)["input_tokens"])
	answers := resp["answers"].([]any)
	require.Len(t, answers, 1)
	require.Equal(t, 0.9, answers[0].(map[string]any)["probability"])

	// The request is metered like any other inference request.
	stats, err := req.GatewayClient.TokenUsageStats(t.Context(), client.TokenUsageStatsOptions{Start: time.Now().Add(-time.Hour), End: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.Totals.Requests)
	require.Equal(t, int64(100), stats.Totals.InputTokens)
	require.Equal(t, int64(2), stats.Totals.OutputTokens)
}

func TestProxyDecisionsChecksModelAccess(t *testing.T) {
	fixture := &decisionFixture{}
	proxy, req := directProxy(t, fixture)
	req.User = &user.DefaultInfo{UID: "8"}
	req.Request = httptest.NewRequest(http.MethodPost, "/api/llm-proxy/v1/decisions", strings.NewReader(`{"model":"m1-test","input":"x","questions":[{"type":"predicate","instructions":"?"}]}`))
	req.SetPathValue("path", "v1/decisions")
	req.ResponseWriter = httptest.NewRecorder()
	err := proxy.proxy(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission")
	require.Nil(t, fixture.request)
}
