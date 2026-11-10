package builtinmcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	gclient "github.com/obot-platform/obot/pkg/gateway/client"
)

const (
	// conversationListLimit bounds the conversations listed for a user.
	conversationListLimit = 100
	// conversationTitleRunes bounds a conversation's title, taken from its first question.
	conversationTitleRunes = 80
)

// SavedMessage is one message of a saved conversation. An answer keeps the tools it
// called, without their results.
type SavedMessage struct {
	Role    string          `json:"role"`
	Content string          `json:"content"`
	Steps   []AssistantStep `json:"steps,omitempty"`
}

// Conversation is a saved Obot AI conversation.
type Conversation struct {
	gclient.AIConversationSummary
	Messages []SavedMessage `json:"messages"`
}

// save adds the question and its answer to the saved conversation, creating it when the
// request starts a new one, and returns its ID. A failure to save is logged; the user still
// gets the answer.
func (a *Assistant) save(ctx context.Context, req api.Context, in AssistantRequest, out AssistantResponse) string {
	userID := req.User.GetUID()
	question := in.Messages[len(in.Messages)-1]
	id := in.ConversationID
	var messages []SavedMessage
	title := ""
	if id != "" {
		_, content, err := req.GatewayClient.GetAIConversation(ctx, userID, id)
		if err != nil {
			slog.Error("Failed to load an Obot AI conversation", "conversation", id, "error", err)
			return id
		}
		title = content.Title
		_ = json.Unmarshal([]byte(content.Messages), &messages)
	} else {
		id = newConversationID()
		// A new conversation started from history the client kept keeps that history.
		for _, m := range in.Messages[:len(in.Messages)-1] {
			messages = append(messages, SavedMessage{Role: m.Role, Content: m.Content})
		}
	}
	if title == "" {
		title = conversationTitle(question.Content)
	}
	steps := make([]AssistantStep, 0, len(out.Steps))
	for _, s := range out.Steps {
		s.Result = ""
		steps = append(steps, s)
	}
	messages = append(messages,
		SavedMessage{Role: "user", Content: question.Content},
		SavedMessage{Role: "assistant", Content: out.Reply, Steps: steps},
	)
	data, err := json.Marshal(messages)
	if err != nil {
		slog.Error("Failed to save an Obot AI conversation", "conversation", id, "error", err)
		return ""
	}
	if err := req.GatewayClient.SaveAIConversation(ctx, userID, id, gclient.AIConversationContent{Title: title, Messages: string(data)}); err != nil {
		slog.Error("Failed to save an Obot AI conversation", "conversation", id, "error", err)
		return ""
	}
	return id
}

// ListConversations lists the caller's saved conversations, most recent first.
func (a *Assistant) ListConversations(req api.Context) error {
	items, err := req.GatewayClient.ListAIConversations(req.Context(), req.User.GetUID(), conversationListLimit)
	if err != nil {
		return err
	}
	return req.Write(map[string]any{"items": items})
}

// GetConversation returns one of the caller's saved conversations.
func (a *Assistant) GetConversation(req api.Context) error {
	summary, content, err := req.GatewayClient.GetAIConversation(req.Context(), req.User.GetUID(), req.PathValue("conversation_id"))
	if err != nil {
		return conversationError(err)
	}
	out := Conversation{AIConversationSummary: summary, Messages: []SavedMessage{}}
	if content.Messages != "" {
		if err := json.Unmarshal([]byte(content.Messages), &out.Messages); err != nil {
			return err
		}
	}
	return req.Write(out)
}

// DeleteConversation deletes one of the caller's saved conversations.
func (a *Assistant) DeleteConversation(req api.Context) error {
	if err := req.GatewayClient.DeleteAIConversation(req.Context(), req.User.GetUID(), req.PathValue("conversation_id")); err != nil {
		return conversationError(err)
	}
	req.WriteHeader(http.StatusNoContent)
	return nil
}

func conversationError(err error) error {
	if errors.Is(err, gclient.ErrAIConversationNotFound) {
		return types.NewErrNotFound("conversation not found")
	}
	return err
}

func newConversationID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "aic-" + hex.EncodeToString(b)
}

// conversationTitle is the first line of the first question, shortened.
func conversationTitle(question string) string {
	title, _, _ := strings.Cut(strings.TrimSpace(question), "\n")
	title = strings.TrimSpace(title)
	if utf8.RuneCountInString(title) > conversationTitleRunes {
		runes := []rune(title)
		title = strings.TrimSpace(string(runes[:conversationTitleRunes-1])) + "…"
	}
	return title
}
