---
title: Obot Agent Management
---

# Obot Agent Management

:::note
Obot Agent features are disabled by default for new deployments, and the **Obot Agent Management** section is hidden while they are disabled. To enable them, set the `OBOT_ENABLE_AGENTS=true` environment variable on the server. Deployments that already had agents before upgrading remain enabled automatically. See the [configuration reference](../configuration/server-configuration.md) for details.
:::

Obot Agent Management provides administrators with tools to configure default agent settings and monitor agent and conversation activity. Access these features from **Obot Agent Management** in the sidebar.

## Token Usage

View token usage across users and models to monitor costs and identify optimization opportunities.

## Model Providers

Add LLM providers and their keys under **Models** → **Providers**. Obot reaches every model through the [LLM Gateway](./llm-gateway.md).

## Model Access Policies

Control which users and groups can access which models in Obot Agent. See [Model Access Policies](./model-access-policies.md) for details.

## Guardrails

Enforce content rules on user prompts, model responses and tool calls, including natural-language LLM judge policies (formerly Message Policies). See [Guardrails](./guardrails.md) for details.
