package client

import (
	"errors"
	"strings"
	"testing"

	"github.com/obot-platform/obot/pkg/gateway/types"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/server/options/encryptionconfig"
	"k8s.io/apiserver/pkg/storage/value"
)

func TestAIConversationsAreTheOwnersAndEncrypted(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(t)
	c.encryptionConfig = &encryptionconfig.EncryptionConfiguration{
		Transformers: map[schema.GroupResource]value.Transformer{credentialGroupResource: testTransformer{}},
	}

	if err := c.SaveAIConversation(ctx, "u1", "aic-1", AIConversationContent{Title: "Spend this week", Messages: `[{"role":"user","content":"hi"}]`}); err != nil {
		t.Fatal(err)
	}
	var row types.AIConversation
	if err := c.db.WithContext(ctx).Where("id = ?", "aic-1").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	for _, v := range row.Payload {
		if strings.Contains(v, "Spend this week") {
			t.Fatalf("the title should be stored encrypted: %v", row.Payload)
		}
	}

	summary, content, err := c.GetAIConversation(ctx, "u1", "aic-1")
	if err != nil || summary.Title != "Spend this week" || content.Messages != `[{"role":"user","content":"hi"}]` {
		t.Fatalf("get: %+v %+v (%v)", summary, content, err)
	}
	if err := c.SaveAIConversation(ctx, "u1", "aic-1", AIConversationContent{Title: "Spend this week", Messages: `[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]`}); err != nil {
		t.Fatal(err)
	}
	if _, content, err := c.GetAIConversation(ctx, "u1", "aic-1"); err != nil || !strings.Contains(content.Messages, "hello") {
		t.Fatalf("an update should replace the messages: %+v (%v)", content, err)
	}
	if _, _, err := c.GetAIConversation(ctx, "u2", "aic-1"); !errors.Is(err, ErrAIConversationNotFound) {
		t.Fatalf("another user must not see the conversation, got %v", err)
	}
	if err := c.SaveAIConversation(ctx, "u2", "aic-1", AIConversationContent{Title: "hijack"}); !errors.Is(err, ErrAIConversationNotFound) {
		t.Fatalf("another user must not overwrite the conversation, got %v", err)
	}

	if err := c.SaveAIConversation(ctx, "u1", "aic-2", AIConversationContent{Title: "Second"}); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListAIConversations(ctx, "u1", 10)
	if err != nil || len(list) != 2 || list[0].ID != "aic-2" {
		t.Fatalf("list, most recent first: %+v (%v)", list, err)
	}
	if others, _ := c.ListAIConversations(ctx, "u2", 10); len(others) != 0 {
		t.Fatalf("another user's list should be empty: %+v", others)
	}

	if err := c.DeleteAIConversation(ctx, "u2", "aic-1"); !errors.Is(err, ErrAIConversationNotFound) {
		t.Fatalf("another user must not delete the conversation, got %v", err)
	}
	if err := c.DeleteAIConversation(ctx, "u1", "aic-1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := c.ListAIConversations(ctx, "u1", 10); len(list) != 1 {
		t.Fatalf("one conversation should remain: %+v", list)
	}
}
