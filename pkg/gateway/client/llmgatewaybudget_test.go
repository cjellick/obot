package client

import (
	"testing"

	"github.com/obot-platform/obot/pkg/gateway/types"
)

func TestLLMGatewayVirtualKeyForUser(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	for vk, subjects := range map[string][]types.LLMGatewayBudgetBinding{
		"vk-everyone": {{SubjectType: types.LLMGatewayBindingSubjectEveryone, SubjectID: "ignored"}},
		"vk-group":    {{SubjectType: types.LLMGatewayBindingSubjectGroup, SubjectID: "eng"}},
		"vk-user":     {{SubjectType: types.LLMGatewayBindingSubjectUser, SubjectID: "alice"}},
	} {
		if err := c.ReplaceLLMGatewayBudgetBindings(ctx, vk, subjects); err != nil {
			t.Fatalf("failed to set bindings for %s: %v", vk, err)
		}
	}

	tests := []struct {
		name   string
		userID string
		groups []string
		want   string
	}{
		{
			name:   "user binding wins over group and everyone",
			userID: "alice",
			groups: []string{"eng"},
			want:   "vk-user",
		},
		{
			name:   "group binding wins over everyone",
			userID: "bob",
			groups: []string{"sales", "eng"},
			want:   "vk-group",
		},
		{
			name:   "everyone binding applies otherwise",
			userID: "carol",
			groups: []string{"sales"},
			want:   "vk-everyone",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.LLMGatewayVirtualKeyForUser(ctx, tt.userID, tt.groups)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}

	// Replacing bindings drops the old ones and clears the ID of everyone bindings.
	if err := c.ReplaceLLMGatewayBudgetBindings(ctx, "vk-everyone", nil); err != nil {
		t.Fatal(err)
	}
	got, err := c.LLMGatewayVirtualKeyForUser(ctx, "carol", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected no binding after removal, got %q", got)
	}

	bindings, err := c.ListLLMGatewayBudgetBindings(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(bindings))
	}

	if err := c.DeleteLLMGatewayBudgetBindings(ctx, "vk-user"); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.LLMGatewayVirtualKeyForUser(ctx, "alice", []string{"eng"}); got != "vk-group" {
		t.Fatalf("expected group binding after deleting user binding, got %q", got)
	}
}
