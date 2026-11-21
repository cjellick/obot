package riskregister

import (
	"strings"

	"github.com/obot-platform/obot/apiclient/types"
)

var (
	// knownClients maps the client names Obot Sentry reports to a segment. Enforced is true
	// for the clients Sentry can block tool calls in; Audited for those it only records.
	knownClients = map[string]aiClient{
		"claude-code": {Display: "Claude Code", Vendor: "Anthropic", Segment: types.RiskSegmentCodingAgent, Enforced: true, Audited: true},
		"codex":       {Display: "Codex", Vendor: "OpenAI", Segment: types.RiskSegmentCodingAgent, Enforced: true, Audited: true},
		"cursor":      {Display: "Cursor", Vendor: "Anysphere", Segment: types.RiskSegmentCodingAgent, Enforced: true, Audited: true},
		"kiro":        {Display: "Kiro", Vendor: "Amazon", Segment: types.RiskSegmentCodingAgent, Enforced: true, Audited: true},
		"vscode":      {Display: "Visual Studio Code", Vendor: "Microsoft", Segment: types.RiskSegmentCodingAgent, Audited: true},
		"opencode":    {Display: "opencode", Segment: types.RiskSegmentCodingAgent},
		"zed":         {Display: "Zed", Vendor: "Zed Industries", Segment: types.RiskSegmentCodingAgent},
		"goose":       {Display: "Goose", Vendor: "Block", Segment: types.RiskSegmentCodingAgent},
		"antigravity": {Display: "Antigravity", Vendor: "Google", Segment: types.RiskSegmentCodingAgent},
		"openclaw":    {Display: "OpenClaw", Segment: types.RiskSegmentCodingAgent},
		"hermes":      {Display: "Hermes", Segment: types.RiskSegmentCodingAgent},
	}

	// knownApps recognizes AI tools among identity provider applications by words in their
	// label. Order matters: the first match wins, so more specific entries come first.
	knownApps = []appPattern{
		{Words: []string{"github copilot"}, App: AIApp{Name: "GitHub Copilot", Vendor: "GitHub", Segment: types.RiskSegmentCodingAgent}},
		{Words: []string{"microsoft 365 copilot"}, App: AIApp{Name: "Microsoft 365 Copilot", Vendor: "Microsoft", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"copilot"}, App: AIApp{Name: "Microsoft Copilot", Vendor: "Microsoft", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"claude code"}, App: AIApp{Name: "Claude Code", Vendor: "Anthropic", Segment: types.RiskSegmentCodingAgent}},
		{Words: []string{"claude"}, App: AIApp{Name: "Claude", Vendor: "Anthropic", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"anthropic"}, App: AIApp{Name: "Claude Console", Vendor: "Anthropic", Segment: types.RiskSegmentAPIWorkload}},
		{Words: []string{"chatgpt"}, App: AIApp{Name: "ChatGPT", Vendor: "OpenAI", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"openai"}, App: AIApp{Name: "OpenAI Platform", Vendor: "OpenAI", Segment: types.RiskSegmentAPIWorkload}},
		{Words: []string{"cursor"}, App: AIApp{Name: "Cursor", Vendor: "Anysphere", Segment: types.RiskSegmentCodingAgent}},
		{Words: []string{"windsurf"}, App: AIApp{Name: "Windsurf", Vendor: "Cognition", Segment: types.RiskSegmentCodingAgent}},
		{Words: []string{"codeium"}, App: AIApp{Name: "Windsurf", Vendor: "Cognition", Segment: types.RiskSegmentCodingAgent}},
		{Words: []string{"tabnine"}, App: AIApp{Name: "Tabnine", Vendor: "Tabnine", Segment: types.RiskSegmentCodingAgent}},
		{Words: []string{"replit"}, App: AIApp{Name: "Replit", Vendor: "Replit", Segment: types.RiskSegmentCodingAgent}},
		{Words: []string{"lovable"}, App: AIApp{Name: "Lovable", Vendor: "Lovable", Segment: types.RiskSegmentCodingAgent}},
		{Words: []string{"gemini"}, App: AIApp{Name: "Gemini", Vendor: "Google", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"perplexity"}, App: AIApp{Name: "Perplexity", Vendor: "Perplexity", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"glean"}, App: AIApp{Name: "Glean", Vendor: "Glean", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"grammarly"}, App: AIApp{Name: "Grammarly", Vendor: "Grammarly", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"otter.ai"}, App: AIApp{Name: "Otter.ai", Vendor: "Otter.ai", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"jasper"}, App: AIApp{Name: "Jasper", Vendor: "Jasper", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"deepseek"}, App: AIApp{Name: "DeepSeek", Vendor: "DeepSeek", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"mistral"}, App: AIApp{Name: "Mistral", Vendor: "Mistral AI", Segment: types.RiskSegmentBusinessUser}},
		{Words: []string{"hugging face"}, App: AIApp{Name: "Hugging Face", Vendor: "Hugging Face", Segment: types.RiskSegmentAPIWorkload}},
		{Words: []string{"openrouter"}, App: AIApp{Name: "OpenRouter", Vendor: "OpenRouter", Segment: types.RiskSegmentAPIWorkload}},
	}
)

// AIApp is an AI tool recognized among identity provider applications.
type AIApp struct {
	Name    string
	Vendor  string
	Segment types.RiskSegment
	// ByModel is true when a model, not the built-in list, recognized the app.
	ByModel bool
}

type aiClient struct {
	Display  string
	Vendor   string
	Segment  types.RiskSegment
	Enforced bool
	Audited  bool
}

type appPattern struct {
	Words []string
	App   AIApp
}

// MatchApp recognizes a well-known AI tool from an application label. It returns nil for
// labels it doesn't know, which a model may then classify.
func MatchApp(label string) *AIApp {
	label = strings.ToLower(label)
	for _, pattern := range knownApps {
		for _, word := range pattern.Words {
			if containsWord(label, word) {
				app := pattern.App
				return &app
			}
		}
	}
	return nil
}

// lookupClient returns what is known about a Sentry client name. Unknown clients are treated
// as coding agents, since Sentry only scans for agent clients.
func lookupClient(name string) aiClient {
	if client, ok := knownClients[strings.ToLower(name)]; ok {
		return client
	}
	return aiClient{Display: name, Segment: types.RiskSegmentCodingAgent}
}

// containsWord reports whether word appears in s on word boundaries, so "cursor" doesn't
// match "precursor".
func containsWord(s, word string) bool {
	for start := 0; ; {
		i := strings.Index(s[start:], word)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(word)
		if (i == 0 || !isWordByte(s[i-1])) && (end == len(s) || !isWordByte(s[end])) {
			return true
		}
		start = i + 1
	}
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}
