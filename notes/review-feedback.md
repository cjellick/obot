# Review feedback (2026-11-09)

Craig's review of the branch, and the decisions from it. Everything here lands on
`obot-2-provenance-rework`: the removals first, then the rest in roughly this order.

## Removals

- [x] Red team: the `pkg/provenance/redteam` package, its API and Dashboard scorecard, the
      Supervisor's red-team filtering, and every mention in copy and notes.
- [x] Honeytokens: the API, storage and UI, the `honeytoken` label, the "Honeytoken exfiltration"
      policy, `action.honeytoken` and the engine check, the Supervisor insight, and the tagger's
      allowed labels.
- [x] Quarantine: the `quarantine` effect, the session flag and the LLM gateway rewrite.
- [x] Warrants: issuing, drift, `session.warrant.*`, the "Outside the declared task" policy and
      the session-page UI.
- [x] The scanner's IP-address rule (IP address means `confidential`).
- [x] Collapse the capabilities that map one to one onto labels. A tool declares
      `confidential`, `untrusted` or `secret` directly, replacing `reads-private`,
      `reads-untrusted` and `credential-bearing`. `exec`, `destructive` and `sends-external`
      stay capabilities.

## Server requests and the built-in MCP servers

- [x] Admins set up the Slack webhook in the UI. When it was set at deploy time, show it read-only.
      Later: a full Slack app, official in Obot Cloud and roll-your-own for self-hosted.
- [x] Snippets that turn the built-in server on by default for Codex (`config.toml` defaults), next
      to Claude's. No allowlists, and no other clients for now.

## Obot AI

- [x] Save chats on the server; users can go back to old ones.
- [ ] Unify the chats: the Dashboard and session "Ask the Supervisor" boxes open Obot AI, with the
      session as context.
- [ ] Much broader tools: let Obot AI do all or most of what the UI can, through the API directly or
      an MCP server that wraps the API sensibly. Design needed.

## Obot's own AI calls

- [ ] Record usage and spend for every internal model call (Obot AI, judge, triage, tagger,
      commentary, policy drafting, tool labels) against the user who triggered it, or a system
      "Obot" identity for background work. Show them in audit logs. No budgets or guardrails on
      them for now.
- [ ] Bounded decisions go through Bifrost's `DecisionRequest`. Use `openai/gpt-6-luna` when
      OpenAI is configured; otherwise Bifrost emulates on `llm-mini`.
  - Tagger: one predicate per label, with per-label thresholds.
  - Tool label suggestions: one predicate per capability.
  - Approval triage: a choice, escalating below a probability threshold.
  - Judge: a choice, with a templated rationale.
  - Delete the unused hand-rolled OpenAI Decisions client.
- [ ] Expose a decisions endpoint on the LLM gateway for outside clients.

## Sessions and supervision

- [ ] Save session commentary and regenerate it only when the session has changed. Make "What
      concerns me" shorter and less padded.
- [ ] Claude environment egress:
  - Don't audit-log Obot's own runner traffic, or at least hide it.
  - Fold Anthropic control-plane traffic into one counter.
  - Turn tunneled paths into real actions (git clone, GitHub MCP, model call).
  - Keep third-party hosts as real egress events.
  - Link egress sessions to the mapped user.
  - Make the Environments audit log page more usable.
  - Demo real outbound traffic being labeled.

## Open

- Admin-defined custom labels with plain-English instructions, checked by the tagger through
  decisions: undecided.
