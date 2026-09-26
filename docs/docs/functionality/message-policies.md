---
title: Message Policies
---

Message policies are now part of [Guardrails](./guardrails.md). A message policy is an **LLM Judge Policy** profile used by a blocking rule, and guardrails can apply it to MCP tool calls as well as to LLM traffic.

When Obot starts, it converts each existing message policy into guardrails, once:

| Message policy | Guardrail |
| --- | --- |
| Display name and definition | An **LLM Judge Policy** profile with the same name and policy |
| Applies to *User Messages* | A rule that blocks on **input** |
| Applies to *Tool Calls* | A rule that blocks on **output** |
| Applies to *Both* | A rule that blocks on input and output |
| Subjects (users, groups, all users) | The rule's assignment |

The original MessagePolicy resources are kept but no longer enforced, and the message policy API is read-only. Earlier violation records stay in the database. New findings, including those from the legacy per-provider LLM routes, are recorded as [guardrail events](./guardrails.md#events).

The `OBOT_SERVER_ENABLE_MESSAGE_POLICIES` setting no longer affects enforcement. Guardrails are always available.

## How LLM judge policies are evaluated

Each policy is evaluated in two stages:

1. A fast screening pass using the `Chat - Fast` default model returns only `yes` or `no`.
2. If the first pass says the content should be blocked, the full `Chat` default model confirms or overturns that result and writes the explanation.

Input rules judge the latest user message in the context of the conversation. Output rules judge the tool calls the model makes. On MCP tool calls, they judge the tool's arguments and results.

Policy evaluation consumes tokens and adds latency. The tokens don't count against users' budgets.

## Writing effective policies

- Be specific about what is disallowed or required.
- Write the rule as a natural language instruction, not as implementation notes.
- Prefer separate policies for distinct concerns instead of one broad policy.
- Use output (tool call) rules for operational constraints, such as limits on purchases, bookings or outbound actions.
- Use input rules for input restrictions, such as prohibited data sharing or disallowed requests.
