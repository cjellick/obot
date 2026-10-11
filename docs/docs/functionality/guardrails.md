---
title: Guardrails
---

## Overview

Guardrails check content flowing through Obot and act on what they find. They apply to:

- **LLM gateway traffic**: requests to models (input) and model responses (output), including streamed responses and tool calls the model makes. Guardrail **rules** check this traffic.
- **MCP traffic**: tool calls and their results, resource contents and prompts, for servers reached through Obot's MCP gateway. [Guardrail filters](./filters.md#guardrail-filters), configured under **MCP Servers > Filters**, check this traffic.

A guardrail can **block** the request or response, **redact** the offending text and let the rest through, or only **log** what it found.

Guardrails are made of:

- **Detectors** (also called profiles) configure one check, such as secrets detection, a PII service or an LLM judge policy, with its settings and credentials. LLM rules and MCP guardrail filters share them.
- **Rules** decide which LLM gateway traffic is checked, by which detectors, for whom, and what happens when a detector finds something.
- **Guardrail filters** do the same for MCP traffic. They are MCP filters, so they use the same server targeting and selectors as other filters and run in the same filter chain. See [Guardrail filters](./filters.md#guardrail-filters).

Where to find them:

| What | Where |
| --- | --- |
| Detectors | **AI Resources > Guardrails > Detectors** |
| Rules for model traffic | **AI Resources > Models > Guardrails** |
| Guardrail filters for MCP traffic | **AI Resources > MCP Servers > Filters** |
| Events for both | **AI Resources > Guardrails > Events** |

## Detectors

| Type | What it detects | Can redact |
| --- | --- | --- |
| **Secrets Detection** (built-in) | API keys and tokens for AWS, GitHub, GitLab, Slack, Stripe, Google, OpenAI, Anthropic, Azure, Twilio, SendGrid, npm, Hugging Face and Obot; private keys, JWTs, database URLs; optionally generic high-entropy secrets. | Yes |
| **Custom Regex** (built-in) | Your RE2 patterns, plus ready-made patterns for email addresses, phone numbers, US SSNs, credit cards (checksum-verified), IP addresses and IBANs. | Yes |
| **LLM Judge Policy** (built-in) | Anything you can describe in plain language, e.g. "Do not help book travel above economy class". Screened by the `Chat - Fast` default model, with a second review by the `Chat` default model before blocking. This is what [message policies](./message-policies.md) became. | No |
| **Microsoft Presidio** | Personal data, using your self-hosted Presidio Analyzer. By default: email addresses, phone numbers, credit cards, US SSNs, ITINs, passports, driver's licenses and bank numbers, IBANs and IP addresses. | Yes |
| **Azure AI Language PII** | Personal and health data. | Yes |
| **Azure AI Content Safety** | Hate, sexual, violence and self-harm content above a severity threshold; optionally prompt injection with Prompt Shields. | No |
| **AWS Bedrock Guardrails** | Whatever the Bedrock guardrail is configured for: denied topics, content and word filters, sensitive information and contextual grounding. | Anonymized content |
| **Google Cloud Model Armor** | Responsible AI filters, prompt injection and jailbreaks, malicious URLs, CSAM and Sensitive Data Protection, per the Model Armor template. | Sensitive data |
| **Check Point AI Agent Security (Lakera Guard)** | Prompt attacks, PII, content violations, unknown links and custom detectors. With a **Project ID**, that project's policy decides what is flagged. Without one, Check Point's default policy screens the content and only results at or above **Minimum confidence** (by default `l2_very_likely`) count, since the default policy also flags some ordinary prompts. | PII, profanity, custom regex |
| **Patronus AI** | Results of Patronus evaluators, such as toxicity and PII detectors or LLM judges with your criteria. | No |
| **CrowdStrike Falcon AIDR** | Malicious prompts, PII, secrets, malicious entities, topics and more, per the collector's policy. | Content AI Guard redacts |

Credentials such as API keys are stored encrypted and are never shown again after they are saved. Leave a credential blank when editing a profile to keep the current value. Changing where a profile's credentials are sent (its endpoint, base URL or region) requires entering them again, so stored credentials can't be redirected to another server.

For a Presidio detector, **Load all types from the service** lists every entity type your Analyzer supports, including its custom recognizers. Types such as `PERSON`, `LOCATION`, `DATE_TIME` and `URL` aren't checked by default, because they match ordinary text, like the names of public figures, cities, "Friday" and links.

Use **Test** on a saved detector to run it against sample text and see what it finds and how the text would be redacted.

A detector can't be deleted while a rule or a guardrail filter uses it.

## Rules

Rules apply to LLM gateway traffic only. To check MCP traffic, create a [guardrail filter](./filters.md#guardrail-filters). Each rule has:

- **Apply to**: input, output or both.
- **Detectors**: one or more detectors to run.
- **Action**:
  - **Block** rejects the request, or ends the response, with a message naming the rule and what was found. You can replace that message with your own.
  - **Redact** replaces what was found with a label such as `[REDACTED:EMAIL]` and lets the rest through. Some detectors can only tell that content is a problem, not where it is. A redact rule blocks when one of those finds something.
  - **Log** only records an event.
- **Assigned to**: users, groups or everyone. A rule with no assignment applies to everyone.
- **Condition** (optional): a [CEL](https://github.com/google/cel-spec) expression that must be true for the rule to apply.
  - It can use `model` (for example `"openai/gpt-4.1"`), `provider`, `params`, `headers`, `user.id` and `user.groups`.
  - For example: `model.startsWith("openai/") && !("contractors" in user.groups)`.
  - Reading a header that isn't there is an error, so test for it first: `"x-team" in headers && headers["x-team"] == "sales"`. A condition that fails to evaluate records an error event (at most once a minute per rule); the rule is then skipped, or blocks the request if it fails closed.
- **Sampling rate**: the percentage of matching traffic to check, for expensive detectors.
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

### MCP gateway

MCP traffic is checked by [guardrail filters](./filters.md#guardrail-filters), which run in-process in the MCP gateway's filter chain, in order with any other filters. See that page for what is checked and how outcomes are audited.

Earlier versions of Obot let a rule target MCP tool calls. Each time Obot starts, any such rule is converted into a guardrail filter that applies to `tools/call` on every MCP server, as the rule did, with the rule's detectors, action, direction (input becomes request, output becomes response), fail closed, sampling rate, assignment, condition and block message. The rule is then deleted. A disabled rule becomes a disabled filter.

## Events

Every finding and every evaluation error is recorded as a guardrail event, for LLM rules and MCP guardrail filters alike. Each event records the rule or filter, detector, target and stage, outcome, user, model or MCP server and tool, and a description of what was found. For a guardrail filter, the rule is the filter's name and ID. Events never include the offending content. They are kept for as long as LLM audit logs.

## Message policies

Message policies are now LLM Judge guardrails. When Obot starts, each existing message policy is converted once into an LLM Judge profile and a blocking rule with the same subjects:

- Policies on **user messages** become input rules.
- Policies on **tool calls** become output rules.
- Policies on **both** become rules for input and output.

The rules fail closed, as message policies did: when the judge's model is unavailable, the request is blocked.

The original policies are kept but no longer enforced. The message policy API is now read-only. Violations found through the legacy per-provider LLM routes are recorded as guardrail events too.

## API

Administrators can manage guardrails under `/api/llm-gateway/guardrails`:

| Endpoint | Purpose |
| --- | --- |
| `GET /providers` | Profile types and their settings |
| `GET, POST /rules`, `GET, PUT, DELETE /rules/{id}` | Rules (`target` must be `llm`) |
| `GET, POST /profiles`, `GET, PUT, DELETE /profiles/{id}` | Detectors (credentials are write-only) |
| `POST /profiles/{id}/test` | Run a detector against sample text |
| `POST /validate-condition` | Check a CEL condition |
| `GET /events` | Events, filterable by `ruleID`, `outcome`, `target`, `userID` and `since` |

Guardrail filters are managed with the filter API, `/api/mcp-webhook-validations`. A guardrail filter has a `guardrail` object in place of a `url`, `mcpServerManifest` or `systemMCPServerCatalogEntryID`:

```json
{
  "name": "No secrets in tool calls",
  "resources": [{ "type": "mcpCatalog", "id": "default" }],
  "selectors": [{ "method": "tools/call" }],
  "guardrail": {
    "detectorIDs": ["<detector id>"],
    "action": "redact",
    "direction": "both",
    "failClosed": false,
    "samplingRate": 100,
    "subjects": [{ "type": "group", "id": "<group id>" }],
    "condition": "mcp_tool != \"search\"",
    "blockMessage": ""
  }
}
```

`action` is `block`, `redact` or `log`. `direction` is `request`, `response` or `both` (the default). `samplingRate` defaults to 100. `subjects` are `user`, `group` or `everyone`; none means everyone. `allowedToMutate` is set from the action (true for `redact`) and doesn't otherwise apply to guardrail filters.

## Related topics

- [LLM Gateway](./llm-gateway.md)
- [MCP Servers](./mcp-servers.md)
- [Filters](./filters.md)
- [Audit Logs and Usage](./audit-logs-and-usage.md)
