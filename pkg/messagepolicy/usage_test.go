package messagepolicy

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/obot-platform/obot/pkg/aiusage"
)

type usageEngine struct {
	err error
}

type capturedCalls struct {
	calls []aiusage.Call
}

func (e usageEngine) ChatCompletion(context.Context, string, []schemas.ChatMessage) (*schemas.BifrostChatResponse, error) {
	return e.response()
}

func (e usageEngine) ChatCompletionWithParams(context.Context, string, []schemas.ChatMessage, *schemas.ChatParameters) (*schemas.BifrostChatResponse, error) {
	return e.response()
}

func (e usageEngine) ChatCompletionStream(_ context.Context, _ string, _ []schemas.ChatMessage, _ *schemas.ChatParameters, onDelta func(*schemas.ChatStreamResponseChoiceDelta)) (*schemas.BifrostLLMUsage, error) {
	text := "hi"
	onDelta(&schemas.ChatStreamResponseChoiceDelta{Content: &text})
	return &schemas.BifrostLLMUsage{PromptTokens: 7, CompletionTokens: 3}, e.err
}

func (e usageEngine) response() (*schemas.BifrostChatResponse, error) {
	if e.err != nil {
		return nil, e.err
	}
	text := `{"ok":true}`
	return &schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{{ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
			Message: &schemas.ChatMessage{Role: schemas.ChatMessageRoleAssistant, Content: &schemas.ChatMessageContent{ContentStr: &text}},
		}}},
		Usage: &schemas.BifrostLLMUsage{PromptTokens: 12, CompletionTokens: 5},
	}, nil
}

func (c *capturedCalls) Record(_ context.Context, call aiusage.Call) {
	c.calls = append(c.calls, call)
}

func TestHelperRecordsEveryModelCall(t *testing.T) {
	recorder := &capturedCalls{}
	h := NewHelper(nil, usageEngine{})
	h.SetUsageRecorder(recorder)

	ctx := aiusage.WithUser(aiusage.WithFeature(t.Context(), aiusage.FeatureObotAI), "u1")
	if _, _, err := h.Complete(ctx, "openai/gpt-5-mini", "system", "user", map[string]any{"type": "object"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.ChatTurn(ctx, "openai/gpt-5-mini", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.ChatTurnStream(ctx, "openai/gpt-5-mini", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(recorder.calls) != 3 {
		t.Fatalf("expected 3 recorded calls, got %d", len(recorder.calls))
	}
	for _, call := range recorder.calls {
		if call.Feature != aiusage.FeatureObotAI || call.UserID != "u1" || call.Model != "openai/gpt-5-mini" || call.Usage == nil {
			t.Fatalf("call %+v", call)
		}
	}
	if recorder.calls[2].Usage.PromptTokens != 7 {
		t.Fatalf("a streamed call keeps the stream's usage: %+v", recorder.calls[2].Usage)
	}

	// Background work without a feature or user is the message policy judge, run by Obot.
	recorder.calls = nil
	h.SetUsageRecorder(recorder)
	failing := NewHelper(nil, usageEngine{err: errors.New("boom")})
	failing.SetUsageRecorder(recorder)
	if _, _, err := failing.ChatTurn(t.Context(), "openai/gpt-5-mini", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if len(recorder.calls) != 1 || recorder.calls[0].Err == nil || recorder.calls[0].UserID != aiusage.SystemUserID || recorder.calls[0].Feature != aiusage.FeatureMessagePolicy {
		t.Fatalf("failed background call %+v", recorder.calls)
	}
}
