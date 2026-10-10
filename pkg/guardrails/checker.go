// Package guardrails checks LLM requests and responses, and MCP tool call arguments and
// results, against administrator-defined rules. Rules decide which traffic is checked and
// what happens on a finding (block, redact or log); profiles configure the checkers that do
// the checking, from built-in regex, secrets detection and LLM-judge policies to external
// services such as Presidio, AWS Bedrock Guardrails or Azure AI Content Safety.
package guardrails

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"github.com/obot-platform/obot/pkg/messagepolicy"
)

const (
	// Stages of a request a rule can check.
	StageInput  = "input"
	StageOutput = "output"

	// Segment roles.
	RoleSystem     = "system"
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleToolCall   = "tool_call"
	RoleToolResult = "tool_result"

	FieldText     FieldType = "text"
	FieldTextarea FieldType = "textarea"
	FieldPassword FieldType = "password"
	FieldNumber   FieldType = "number"
	FieldBool     FieldType = "bool"
	FieldSelect   FieldType = "select"
	FieldList     FieldType = "list"
)

var (
	registryLock sync.RWMutex
	registry     = map[string]registration{}
)

// Segment is one piece of text a checker inspects and, for redacting rules, may rewrite.
type Segment struct {
	Role string
	Text string
	// Name is the tool name for tool calls and tool results.
	Name string
}

// CheckInput is what a checker evaluates.
type CheckInput struct {
	Target string
	Stage  string
	// Segments is the content being checked, in conversation order.
	Segments []Segment
	// History is earlier conversation that gives context to checkers that use it, such as
	// the LLM judge. It is never rewritten.
	History []Segment
}

// Span locates text a checker wants redacted, as byte offsets into a segment's text.
type Span struct {
	Segment int
	Start   int
	End     int
	Label   string
}

// Finding is one problem a checker found.
type Finding struct {
	Category string  `json:"category"`
	Message  string  `json:"message,omitempty"`
	Score    float64 `json:"score,omitempty"`
	// Spans are the text ranges the finding covers. Checkers that can't locate findings
	// leave them empty, and redacting rules then block instead.
	Spans []Span `json:"-"`
	// Count is how many matches the finding covers.
	Count int `json:"count,omitempty"`
	// Explanation is shown to the caller when the finding blocks, but is never stored: it
	// is free text, such as an LLM judge's explanation, that may quote the content.
	Explanation string `json:"-"`
}

// Result is a checker's verdict. Any finding is a violation.
type Result struct {
	Findings []Finding
	// Replacements optionally replaces whole segments with provider-supplied redacted text
	// (for checkers whose service returns redacted content rather than spans).
	Replacements map[int]string
}

// Checker evaluates content.
type Checker interface {
	Check(ctx context.Context, in CheckInput) (Result, error)
}

// FieldType is how a profile field is edited.
type FieldType string

// Field describes one profile setting, for validation and for the UI's forms.
type Field struct {
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	Description string    `json:"description,omitempty"`
	Type        FieldType `json:"type"`
	Options     []string  `json:"options,omitempty"`
	Default     any       `json:"default,omitempty"`
	Required    bool      `json:"required,omitempty"`
	// Sensitive fields are stored encrypted and never returned by the API.
	Sensitive bool `json:"sensitive,omitempty"`
	// Destination fields decide where the profile's credentials are sent, such as an
	// endpoint URL or a region that is part of a host name. Changing one requires
	// re-entering the credentials, so stored credentials can't be redirected to another host.
	Destination bool `json:"destination,omitempty"`
}

// ProviderInfo describes a checker type.
type ProviderInfo struct {
	Type        string  `json:"type"`
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Category    string  `json:"category"`
	Fields      []Field `json:"fields"`
	// Redacts reports whether the checker can locate findings for redaction.
	Redacts bool `json:"redacts"`
}

// Deps are services checkers may use.
type Deps struct {
	// Judge evaluates natural-language policies with an LLM.
	Judge *messagepolicy.Helper
	// HTTPClient is used for calls to external guardrail services.
	HTTPClient *http.Client
}

// Factory builds a checker from a profile's settings.
type Factory func(config map[string]any, secrets map[string]string, deps Deps) (Checker, error)

type registration struct {
	info    ProviderInfo
	factory Factory
}

// Register adds a checker type. It is called from init functions.
func Register(info ProviderInfo, factory Factory) {
	registryLock.Lock()
	defer registryLock.Unlock()
	if _, exists := registry[info.Type]; exists {
		panic("guardrail checker type registered twice: " + info.Type)
	}
	registry[info.Type] = registration{info: info, factory: factory}
}

// Providers returns every registered checker type, sorted by label.
func Providers() []ProviderInfo {
	registryLock.RLock()
	defer registryLock.RUnlock()
	result := make([]ProviderInfo, 0, len(registry))
	for _, r := range registry {
		result = append(result, r.info)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Category != result[j].Category {
			return result[i].Category < result[j].Category
		}
		return result[i].Label < result[j].Label
	})
	return result
}

// Provider returns a registered checker type.
func Provider(typ string) (ProviderInfo, bool) {
	registryLock.RLock()
	defer registryLock.RUnlock()
	r, ok := registry[typ]
	return r.info, ok
}

func newChecker(typ string, config map[string]any, secrets map[string]string, deps Deps) (Checker, error) {
	registryLock.RLock()
	r, ok := registry[typ]
	registryLock.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown guardrail type %q", typ)
	}
	for _, f := range r.info.Fields {
		if !f.Required {
			continue
		}
		if f.Sensitive {
			if secrets[f.Name] == "" {
				return nil, fmt.Errorf("%s is required", f.Label)
			}
		} else if v, ok := config[f.Name]; !ok || v == nil || v == "" {
			return nil, fmt.Errorf("%s is required", f.Label)
		}
	}
	return r.factory(config, secrets, deps)
}

// ConfigString reads a string setting.
func ConfigString(config map[string]any, key, def string) string {
	switch v := config[key].(type) {
	case string:
		if v != "" {
			return v
		}
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return def
}

// ConfigBool reads a boolean setting.
func ConfigBool(config map[string]any, key string, def bool) bool {
	switch v := config[key].(type) {
	case bool:
		return v
	case string:
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// ConfigFloat reads a numeric setting.
func ConfigFloat(config map[string]any, key string, def float64) float64 {
	switch v := config[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// ConfigStrings reads a list setting.
func ConfigStrings(config map[string]any, key string) []string {
	switch v := config[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}
