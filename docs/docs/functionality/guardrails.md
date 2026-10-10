---
title: Guardrails
---

## Overview

Guardrails check content flowing through Obot and act on what they find. They apply to:

- **LLM gateway traffic**: requests to models (input) and model responses (output), including streamed responses and tool calls the model makes.
- **MCP tool calls**: the arguments sent to an MCP server's tool (input) and the result the tool returns (output), for servers reached through Obot's MCP gateway.

A guardrail can **block** the request or response, **redact** the offending text and let the rest through, or only **log** what it found. Guardrails are managed under **LLM Gateway > Guardrails**.

Guardrails are made of two things:

- **Profiles** configure one detector, such as secrets detection, a PII service or an LLM judge policy, with its settings and credentials.
- **Rules** decide which traffic is checked, by which profiles, for whom, and what happens when a profile finds something.

## Profiles

| Type | What it detects | Can redact |
| --- | --- | --- |
| **Secrets Detection** (built-in) | API keys and tokens for AWS, GitHub, GitLab, Slack, Stripe, Google, OpenAI, Anthropic, Azure, Twilio, SendGrid, npm, Hugging Face and Obot; private keys, JWTs, database URLs; optionally generic high-entropy secrets. | Yes |
| **Custom Regex** (built-in) | Your RE2 patterns, plus ready-made patterns for email addresses, phone numbers, US SSNs, credit cards (checksum-verified), IP addresses and IBANs. | Yes |
| **LLM Judge Policy** (built-in) | Anything you can describe in plain language, e.g. "Do not help book travel above economy class". Screened by the `Chat - Fast` default model, with a second review by the `Chat` default model before blocking. This is what [message policies](./message-policies.md) became. | No |
| **Microsoft Presidio** | Personal data, using your self-hosted Presidio Analyzer. | Yes |
| **Azure AI Language PII** | Personal and health data. | Yes |
| **Azure AI Content Safety** | Hate, sexual, violence and self-harm content above a severity threshold; optionally prompt injection with Prompt Shields. | No |
| **AWS Bedrock Guardrails** | Whatever the Bedrock guardrail is configured for: denied topics, content and word filters, sensitive information and contextual grounding. | Anonymized content |
| **Google Cloud Model Armor** | Responsible AI filters, prompt injection and jailbreaks, malicious URLs, CSAM and Sensitive Data Protection, per the Model Armor template. | Sensitive data |
| **Check Point AI Agent Security (Lakera Guard)** | Prompt attacks, PII, content violations, unknown links and custom detectors. | PII, profanity, custom regex |
| **Patronus AI** | Results of Patronus evaluators, such as toxicity and PII detectors or LLM judges with your criteria. | No |
| **CrowdStrike Falcon AIDR** | Malicious prompts, PII, secrets, malicious entities, topics and more, per the collector's policy. | Content AI Guard redacts |

Credentials such as API keys are stored encrypted and are never shown again after they are saved. Leave a credential blank when editing a profile to keep the current value. Changing where a profile's credentials are sent (its endpoint, base URL or region) requires entering them again, so stored credentials can't be redirected to another server.

Use **Test** on a saved profile to run it against sample text and see what it finds and how the text would be redacted.

## Rules

Each rule has:

- **Target**: LLM gateway traffic or MCP tool calls.
- **Apply to**: input, output or both.
- **Profiles**: one or more profiles to run.
- **Action**:
  - **Block** rejects the request, or ends the response, with a message naming the rule and what was found. You can replace that message with your own.
  - **Redact** replaces what was found with a label such as `[REDACTED:EMAIL]` and lets the rest through. Some profiles can only tell that content is a problem, not where it is. A redact rule blocks when one of those finds something.
  - **Log** only records an event.
- **Assigned to**: users, groups or everyone. A rule with no assignment applies to everyone.
- **Condition** (optional): a [CEL](https://github.com/google/cel-spec) expression that must be true for the rule to apply.
  - For LLM traffic it can use `model` (for example `"openai/gpt-4.1"`), `provider`, `params` and `headers`.
  - For MCP tool calls it can use `mcp_server`, `mcp_tool` and `mcp_arguments`, as well as `headers`.
  - Both can use `user.id` and `user.groups`.
  - For example: `mcp_server == "GitHub" && mcp_tool.startsWith("create_")`.
  - Reading a header or argument that isn't there is an error, so test for it first: `"x-team" in headers && headers["x-team"] == "sales"`. A condition that fails to evaluate records an error event (at most once a minute per rule); the rule is then skipped, or blocks the request if it fails closed.
- **Sampling rate**: the percentage of matching traffic to check, for expensive profiles.
- **Input scope**: whether input checks look at the whole conversation or only what was added since the model last replied. Redacting rules always check the whole conversation, so nothing unredacted is sent.
- **Fail closed**: whether to block when a profile or the rule's condition can't be evaluated, for example because an external service or the LLM judge's model is down. By default the rule is skipped and an error event is recorded.

Rules run in the order they were created. A block stops evaluation. Each redaction is applied before the next rule runs.

## How checks are applied

### LLM gateway

Input rules run before anything else sees the request, so neither the provider nor the gateway's request log sees content before it is redacted. Output rules run after the response arrives and before it is logged or returned. Token counting requests (`/v1/messages/count_tokens`, `/v1/responses/input_tokens`) carry the whole prompt to the provider and are checked as input. Obot's own [LLM audit log](./audit-logs-and-usage.md) records requests as clients sent them, before redaction.

Checks cover message text, tool calls and tool results, and also reasoning (thinking) and refusal text. Redacting signed reasoning, such as Anthropic extended thinking, invalidates its signature, so the provider may reject a later request that sends that reasoning back.

Some clients, notably Claude Code, normally have their requests forwarded to Anthropic as sent. When any guardrail applies to a request, Obot instead sends the request it checked, and returns the response it checked, so nothing bypasses the rules.

For **streamed responses**, Obot holds the stream while a rule that can block or redact applies. It checks the complete response, then delivers it at once: redacted if needed, or replaced by an error if blocked. A secret split across stream chunks is still found. Log-only rules don't hold the stream. If a stream can't be held, a finding that would block or redact ends the stream and is recorded as an error, since the content has already been delivered.

A blocked request gets HTTP 400 with an error of type `guardrail_intervention`.

### MCP tool calls

Guardrails run after any MCP hooks configured for the server. For input, every string in the tool call's arguments is checked. For output, the tool result's text content, embedded text resources and structured content are checked. Images and other binary content are not checked.

A blocked tool call or result is returned to the client as a JSON-RPC error, and the MCP server is never called for a blocked tool call. Results are checked whichever stream they arrive on, including a GET stream when a client resumes a stream or uses the legacy SSE transport. While any MCP guardrail rule exists, JSON-RPC batches are refused, and so are tool calls whose parameters can't be parsed. Guardrail outcomes appear with hook results in the MCP audit log, which records both the original and the redacted message.

For composite MCP servers, `mcp_server` is the composite server's name and `mcp_tool` is the tool name as the composite server exposes it.

## Events

Every finding and every evaluation error is recorded under **Guardrails > Events**. Each event records the rule, profile, target and stage, outcome, user, model or MCP server and tool, and a description of what was found. Events never include the offending content. They are kept for as long as LLM audit logs.

## Message policies

Message policies are now LLM Judge guardrails. When Obot starts, each existing message policy is converted once into an LLM Judge profile and a blocking rule with the same subjects:

- Policies on **user messages** become input rules.
- Policies on **tool calls** become output rules.
- Policies on **both** become rules for input and output.

The original policies are kept but no longer enforced. The message policy API is now read-only. Violations found through the legacy per-provider LLM routes are recorded as guardrail events too.

## API

Administrators can manage guardrails under `/api/llm-gateway/guardrails`:

| Endpoint | Purpose |
| --- | --- |
| `GET /providers` | Profile types and their settings |
| `GET, POST /rules`, `GET, PUT, DELETE /rules/{id}` | Rules |
| `GET, POST /profiles`, `GET, PUT, DELETE /profiles/{id}` | Profiles (credentials are write-only) |
| `POST /profiles/{id}/test` | Run a profile against sample text |
| `POST /validate-condition` | Check a CEL condition |
| `GET /events` | Events, filterable by `ruleID`, `outcome`, `target`, `userID` and `since` |

## Related topics

- [LLM Gateway](./llm-gateway.md)
- [MCP Servers](./mcp-servers.md)
- [Audit Logs and Usage](./audit-logs-and-usage.md)
