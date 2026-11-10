package claudecredentials

import (
	"strings"
)

// Egress categories say what a Claude Environment request is, so the session graph records
// what the agent did rather than the plumbing that keeps it running, and the audit log can
// hide that plumbing.
const (
	// egressRunner is Obot's own runner reporting the session's status to Obot.
	egressRunner = "runner"
	// egressObot is any other request to Obot, such as through its LLM or MCP gateway, which
	// record it themselves.
	egressObot = "obot"
	// egressControlPlane is Anthropic's platform keeping the cloud session running:
	// heartbeats, worker sync, runner polling, sign-in and telemetry.
	egressControlPlane = "control-plane"
	// egressModel is a model call to Anthropic.
	egressModel = "model"
	// egressGit is git traffic Anthropic's git proxy carries to a repository host.
	egressGit = "git"
	// egressMCP is an MCP server Anthropic relays, such as GitHub's.
	egressMCP = "mcp"
	// egressExternal is everything else: the agent reaching a host directly.
	egressExternal = "egress"
)

// egressClass is what a request is and, for traffic tunnelled through Anthropic, where it
// really goes.
type egressClass struct {
	category string
	// host and path are the real destination: for git, the repository host and the
	// repository.
	host, path string
	// server names the relayed MCP server.
	server string
	// summary describes the request for the session timeline.
	summary string
	// push reports a git push.
	push bool
}

// background reports whether the request is plumbing rather than something the agent did.
func (c egressClass) background() bool {
	switch c.category {
	case egressRunner, egressObot, egressControlPlane:
		return true
	}
	return false
}

// classifyEgress classifies a request by its host and path. path may carry a query.
func classifyEgress(obotURL, host, path string) egressClass {
	pathOnly, query, _ := strings.Cut(path, "?")
	switch {
	case sameHost(obotURL, host):
		if strings.HasPrefix(pathOnly, "/api/claude-runners/") {
			return egressClass{category: egressRunner}
		}
		return egressClass{category: egressObot}
	case strings.EqualFold(host, anthropicAPIHost):
		return classifyAnthropic(pathOnly, query)
	}
	return egressClass{category: egressExternal}
}

func classifyAnthropic(path, query string) egressClass {
	if path == "/v1/messages" || strings.HasPrefix(path, "/v1/messages/") {
		return egressClass{category: egressModel, summary: "model call to Anthropic"}
	}
	if rest, ok := strings.CutPrefix(path, "/v1/code/agent-proxy/git/"); ok {
		return classifyGit(rest, query)
	}
	if strings.HasPrefix(path, "/v2/ccr-sessions/") {
		parts := strings.Split(strings.Trim(path, "/"), "/")
		// /v2/ccr-sessions/<session>/<server>/mcp
		if len(parts) == 5 && parts[4] == "mcp" && parts[3] != "" {
			return egressClass{category: egressMCP, server: parts[3]}
		}
	}
	return egressClass{category: egressControlPlane}
}

// classifyGit reads <host>/<owner>/<repo>[.git]/<smart HTTP endpoint>.
func classifyGit(rest, query string) egressClass {
	parts := strings.Split(rest, "/")
	if len(parts) < 4 {
		return egressClass{category: egressGit, summary: "git"}
	}
	host := parts[0]
	repo := strings.TrimSuffix(strings.Join(parts[1:3], "/"), ".git")
	endpoint := strings.Join(parts[3:], "/")
	push := endpoint == "git-receive-pack" || strings.Contains(query, "service=git-receive-pack")
	verb := "git fetch"
	if push {
		verb = "git push"
	}
	return egressClass{
		category: egressGit,
		host:     host,
		path:     "/" + repo,
		summary:  verb + " " + host + "/" + repo,
		push:     push,
	}
}
