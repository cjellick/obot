---
title: Obot Agent
---

# Obot Agent

Obot Agent is a chat interface built to work directly with MCP. It provides a conversational way for users to interact with MCP servers and accomplish tasks using AI.

:::note
Obot Agent features are disabled by default for new deployments. To enable them, set the `OBOT_ENABLE_AGENTS=true` environment variable on the server. Deployments that already had agents before upgrading remain enabled automatically. See the [configuration reference](../configuration/server-configuration.md) for details.
:::

## Key Concepts

### Conversations

Conversations provide isolated message history while sharing the agent's configuration and resources.

### Workflows

Workflows automate interactions through scheduled or on-demand execution. They can run on recurring schedules or be triggered manually.

### Models

Obot Agent reaches models through the [LLM Gateway](../functionality/llm-gateway.md), which supports providers including OpenAI, Anthropic, Azure OpenAI, Amazon Bedrock, and Google Vertex AI. Administrators add providers under **Models** → **Providers** and make models available to users with [Model Access Policies](../functionality/model-access-policies.md).

## Learn More

- [Obot Agent Management](../functionality/obot-agent-management.md) - Configure default agent, conversation, and workflow settings
