package claudecredentials

import (
	"testing"
)

func TestClassifyEgress(t *testing.T) {
	const obot = "https://obot.example.com"
	tests := []struct {
		name     string
		host     string
		path     string
		category string
		summary  string
		host2    string
		path2    string
		server   string
		push     bool
	}{
		{name: "runner status", host: "obot.example.com", path: "/api/claude-runners/acr-1/status", category: egressRunner},
		{name: "other Obot traffic", host: "obot.example.com", path: "/llm/v1/messages", category: egressObot},
		{name: "model call", host: "api.anthropic.com", path: "/v1/messages?beta=true", category: egressModel, summary: "model call to Anthropic"},
		{name: "heartbeat", host: "api.anthropic.com", path: "/v1/code/sessions/cse_1/worker/heartbeat", category: egressControlPlane},
		{name: "runner polling", host: "api.anthropic.com", path: "/v1/code/runners/self-hosted/runners/ccrunner_1/poll", category: egressControlPlane},
		{name: "telemetry", host: "api.anthropic.com", path: "/api/event_logging/v2/batch", category: egressControlPlane},
		{
			name:     "git fetch",
			host:     "api.anthropic.com",
			path:     "/v1/code/agent-proxy/git/github.com/cjellick/obot/info/refs?service=git-upload-pack",
			category: egressGit,
			summary:  "git fetch github.com/cjellick/obot",
			host2:    "github.com",
			path2:    "/cjellick/obot",
		},
		{
			name:     "git push",
			host:     "api.anthropic.com",
			path:     "/v1/code/agent-proxy/git/github.com/cjellick/obot.git/git-receive-pack",
			category: egressGit,
			summary:  "git push github.com/cjellick/obot",
			host2:    "github.com",
			path2:    "/cjellick/obot",
			push:     true,
		},
		{name: "relayed MCP", host: "api.anthropic.com", path: "/v2/ccr-sessions/cse_1/github/mcp?native=1", category: egressMCP, server: "github"},
		{name: "direct egress", host: "pypi.org", path: "/simple/requests/", category: egressExternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyEgress(obot, tt.host, tt.path)
			if got.category != tt.category || got.summary != tt.summary || got.host != tt.host2 || got.path != tt.path2 || got.server != tt.server || got.push != tt.push {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
