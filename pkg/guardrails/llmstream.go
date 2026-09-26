package guardrails

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// streamPart is one piece of streamed content: an assistant text or a tool call's arguments.
type streamPart struct {
	role string
	name string
	text strings.Builder
}

// streamState accumulates a streamed response so the complete text can be checked. While
// the stream is held, content is blanked as it passes through, and once checked the held
// chunks are rewritten: the first chunk of each part carries the part's complete (possibly
// redacted) text and later chunks carry none. Clients see the same content, delivered at
// once after the check.
type streamState struct {
	held  bool
	order []string
	parts map[string]*streamPart
	// names maps Responses output item IDs to their function names.
	names map[string]string
}

func newStreamState() *streamState {
	return &streamState{parts: map[string]*streamPart{}, names: map[string]string{}}
}

func (s *streamState) part(key, role, name string) *streamPart {
	p, ok := s.parts[key]
	if !ok {
		p = &streamPart{role: role, name: name}
		s.parts[key] = p
		s.order = append(s.order, key)
	}
	if p.name == "" {
		p.name = name
	}
	return p
}

func chatTextKey(choice int) string { return fmt.Sprintf("c%d", choice) }

func chatToolKey(choice int, index uint16) string { return fmt.Sprintf("c%d:t%d", choice, index) }

func respTextKey(itemID string, contentIndex int) string {
	return fmt.Sprintf("i:%s:%d", itemID, contentIndex)
}

func respArgsKey(itemID string) string { return "a:" + itemID }

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// observe records a chunk's original content.
func (s *streamState) observe(resp *schemas.BifrostResponse) {
	switch {
	case resp.ChatResponse != nil:
		for _, c := range resp.ChatResponse.Choices {
			if c.ChatStreamResponseChoice == nil || c.Delta == nil {
				continue
			}
			if c.Delta.Content != nil {
				s.part(chatTextKey(c.Index), RoleAssistant, "").text.WriteString(*c.Delta.Content)
			}
			for _, tc := range c.Delta.ToolCalls {
				s.part(chatToolKey(c.Index, tc.Index), RoleToolCall, derefString(tc.Function.Name)).text.WriteString(tc.Function.Arguments)
			}
		}
	case resp.ResponsesStreamResponse != nil:
		ev := resp.ResponsesStreamResponse
		itemID := derefString(ev.ItemID)
		switch ev.Type {
		case schemas.ResponsesStreamResponseTypeOutputItemAdded:
			if ev.Item != nil && ev.Item.ID != nil && ev.Item.ResponsesToolMessage != nil && ev.Item.Name != nil {
				s.names[*ev.Item.ID] = *ev.Item.Name
			}
		case schemas.ResponsesStreamResponseTypeOutputTextDelta:
			s.part(respTextKey(itemID, derefInt(ev.ContentIndex)), RoleAssistant, "").text.WriteString(derefString(ev.Delta))
		case schemas.ResponsesStreamResponseTypeOutputTextDone:
			// Some providers only send the complete text.
			if p := s.part(respTextKey(itemID, derefInt(ev.ContentIndex)), RoleAssistant, ""); p.text.Len() == 0 {
				p.text.WriteString(derefString(ev.Text))
			}
		case schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDelta:
			s.part(respArgsKey(itemID), RoleToolCall, s.names[itemID]).text.WriteString(derefString(ev.Delta))
		case schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDone:
			if p := s.part(respArgsKey(itemID), RoleToolCall, s.names[itemID]); p.text.Len() == 0 {
				p.text.WriteString(derefString(ev.Arguments))
			}
		}
	}
}

func (s *streamState) segments() []Segment {
	out := make([]Segment, 0, len(s.order))
	for _, key := range s.order {
		p := s.parts[key]
		if p.text.Len() == 0 {
			continue
		}
		out = append(out, Segment{Role: p.role, Name: p.name, Text: p.text.String()})
	}
	return out
}

// finalText maps each part to the text clients receive.
func (s *streamState) finalText(d Decision) map[string]string {
	out := map[string]string{}
	i := 0
	for _, key := range s.order {
		p := s.parts[key]
		if p.text.Len() == 0 {
			continue
		}
		text := p.text.String()
		if d.Redacted && i < len(d.Segments) {
			text = d.Segments[i].Text
		}
		out[key] = text
		i++
	}
	return out
}

// blank returns a copy of a chunk with its content removed.
func (s *streamState) blank(resp *schemas.BifrostResponse) *schemas.BifrostResponse {
	out := *resp
	out.ChatResponse = s.rewriteChat(resp.ChatResponse, nil, nil)
	out.ResponsesStreamResponse = s.rewriteResponsesEvent(resp.ResponsesStreamResponse, nil, nil)
	return &out
}

func (s *streamState) rewriteChunk(c *schemas.BifrostStreamChunk, final map[string]string, emitted map[string]bool) *schemas.BifrostStreamChunk {
	if c == nil {
		return nil
	}
	out := *c
	out.BifrostChatResponse = s.rewriteChat(c.BifrostChatResponse, final, emitted)
	out.BifrostResponsesStreamResponse = s.rewriteResponsesEvent(c.BifrostResponsesStreamResponse, final, emitted)
	return &out
}

func (s *streamState) rewriteResponse(resp *schemas.BifrostResponse, final map[string]string, emitted map[string]bool) *schemas.BifrostResponse {
	out := *resp
	out.ChatResponse = s.rewriteChat(resp.ChatResponse, final, emitted)
	out.ResponsesStreamResponse = s.rewriteResponsesEvent(resp.ResponsesStreamResponse, final, emitted)
	return &out
}

// deltaText returns what a delta for key carries: nothing while blanking (final == nil),
// the complete text for the first delta of a part, and nothing for later deltas.
func deltaText(key string, final map[string]string, emitted map[string]bool, original string) string {
	if final == nil {
		return ""
	}
	text, ok := final[key]
	if !ok {
		return original
	}
	if emitted[key] {
		return ""
	}
	emitted[key] = true
	return text
}

// fullText returns what a complete-text field for key carries.
func fullText(key string, final map[string]string, original string) string {
	if final == nil {
		return ""
	}
	if text, ok := final[key]; ok {
		return text
	}
	return original
}

func (s *streamState) rewriteChat(r *schemas.BifrostChatResponse, final map[string]string, emitted map[string]bool) *schemas.BifrostChatResponse {
	if r == nil {
		return nil
	}
	out := *r
	out.Choices = make([]schemas.BifrostResponseChoice, len(r.Choices))
	for i, c := range r.Choices {
		out.Choices[i] = c
		if c.ChatStreamResponseChoice == nil || c.Delta == nil {
			continue
		}
		delta := *c.Delta
		if delta.Content != nil {
			text := deltaText(chatTextKey(c.Index), final, emitted, *delta.Content)
			delta.Content = &text
		}
		if len(delta.ToolCalls) > 0 {
			delta.ToolCalls = make([]schemas.ChatAssistantMessageToolCall, len(c.Delta.ToolCalls))
			for j, tc := range c.Delta.ToolCalls {
				tc.Function.Arguments = deltaText(chatToolKey(c.Index, tc.Index), final, emitted, tc.Function.Arguments)
				delta.ToolCalls[j] = tc
			}
		}
		out.Choices[i].ChatStreamResponseChoice = &schemas.ChatStreamResponseChoice{Delta: &delta}
	}
	return &out
}

func (s *streamState) rewriteResponsesEvent(ev *schemas.BifrostResponsesStreamResponse, final map[string]string, emitted map[string]bool) *schemas.BifrostResponsesStreamResponse {
	if ev == nil {
		return nil
	}
	out := *ev
	itemID := derefString(ev.ItemID)
	switch ev.Type {
	case schemas.ResponsesStreamResponseTypeOutputTextDelta:
		text := deltaText(respTextKey(itemID, derefInt(ev.ContentIndex)), final, emitted, derefString(ev.Delta))
		out.Delta = &text
	case schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDelta:
		text := deltaText(respArgsKey(itemID), final, emitted, derefString(ev.Delta))
		out.Delta = &text
	case schemas.ResponsesStreamResponseTypeOutputTextDone:
		if ev.Text != nil {
			text := fullText(respTextKey(itemID, derefInt(ev.ContentIndex)), final, *ev.Text)
			out.Text = &text
		}
	case schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDone:
		if ev.Arguments != nil {
			text := fullText(respArgsKey(itemID), final, *ev.Arguments)
			out.Arguments = &text
		}
	case schemas.ResponsesStreamResponseTypeContentPartDone:
		if ev.Part != nil && ev.Part.Text != nil {
			part := *ev.Part
			text := fullText(respTextKey(itemID, derefInt(ev.ContentIndex)), final, *ev.Part.Text)
			part.Text = &text
			out.Part = &part
		}
	}
	if ev.Item != nil {
		item := s.rewriteItem(*ev.Item, final)
		out.Item = &item
	}
	if ev.Response != nil {
		resp := *ev.Response
		resp.Output = make([]schemas.ResponsesMessage, len(ev.Response.Output))
		for i, item := range ev.Response.Output {
			resp.Output[i] = s.rewriteItem(item, final)
		}
		out.Response = &resp
	}
	return &out
}

// rewriteItem replaces the complete text of a Responses output item.
func (s *streamState) rewriteItem(item schemas.ResponsesMessage, final map[string]string) schemas.ResponsesMessage {
	id := derefString(item.ID)
	if item.ResponsesToolMessage != nil && item.Arguments != nil {
		tm := *item.ResponsesToolMessage
		text := fullText(respArgsKey(id), final, *item.Arguments)
		tm.Arguments = &text
		item.ResponsesToolMessage = &tm
	}
	if item.Content != nil {
		content := *item.Content
		if content.ContentStr != nil {
			text := fullText(respTextKey(id, 0), final, *content.ContentStr)
			content.ContentStr = &text
		}
		if len(content.ContentBlocks) > 0 {
			content.ContentBlocks = make([]schemas.ResponsesMessageContentBlock, len(item.Content.ContentBlocks))
			for i, b := range item.Content.ContentBlocks {
				if b.Text != nil {
					text := fullText(respTextKey(id, i), final, *b.Text)
					b.Text = &text
				}
				content.ContentBlocks[i] = b
			}
		}
		item.Content = &content
	}
	return item
}
