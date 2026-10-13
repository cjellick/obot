import type { Model } from '$lib/services';
import GatewayConnect from './GatewayConnect.svelte';
import { describe, expect, it } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

const obotURL = 'https://obot.example.com';

const models = [
	{
		id: 'm1',
		name: 'gpt',
		targetModel: 'openai/gpt-4o',
		dialect: 'OpenAIResponses'
	},
	{
		id: 'm2',
		name: 'claude',
		targetModel: 'anthropic/claude-opus-4-5',
		dialect: 'AnthropicMessages'
	}
] as unknown as Model[];

describe('GatewayConnect', () => {
	it('starts on Claude Code pointed at this Obot server', async () => {
		render(GatewayConnect, { obotURL });

		await expect
			.element(page.getByRole('tab', { name: 'Claude Code' }))
			.toHaveAttribute('aria-selected', 'true');
		await expect
			.element(
				page.getByText('export ANTHROPIC_BASE_URL="https://obot.example.com/api/llm-proxy"', {
					exact: false
				})
			)
			.toBeInTheDocument();
		await expect
			.element(
				page.getByText('obot login --url https://obot.example.com --scope llm --print-token', {
					exact: false
				})
			)
			.toBeInTheDocument();
		// Claude Code picks its own Claude models; no model is set.
		await expect
			.element(page.getByText('ANTHROPIC_MODEL', { exact: false }))
			.not.toBeInTheDocument();
		// Without Claude subscriptions allowed, there is only the API key setup.
		await expect
			.element(page.getByText('ANTHROPIC_CUSTOM_HEADERS', { exact: false }))
			.not.toBeInTheDocument();
	});

	it('leaves the model to Claude Code when the gateway has Anthropic models', async () => {
		render(GatewayConnect, { obotURL, models });

		await expect
			.element(page.getByText('export ANTHROPIC_BASE_URL', { exact: false }))
			.toBeInTheDocument();
		await expect
			.element(page.getByText('ANTHROPIC_MODEL', { exact: false }))
			.not.toBeInTheDocument();
	});

	it('names a gateway model for Claude Code without an Anthropic provider', async () => {
		const bedrockOnly = [
			{
				id: 'm3',
				name: 'embed',
				targetModel: 'bedrock/amazon.titan-embed-text-v2:0',
				usage: 'text-embedding'
			},
			{
				id: 'm4',
				name: 'nova',
				targetModel: 'bedrock/amazon.nova-pro-v1:0',
				usage: 'llm'
			},
			{
				id: 'm5',
				name: 'claude',
				targetModel: 'bedrock/anthropic.claude-sonnet-4-5-20250929-v1:0',
				usage: 'llm'
			}
		] as unknown as Model[];
		render(GatewayConnect, { obotURL, models: bedrockOnly });

		await expect
			.element(
				page.getByText(
					'export ANTHROPIC_MODEL="bedrock/anthropic.claude-sonnet-4-5-20250929-v1:0"',
					{
						exact: false
					}
				)
			)
			.toBeInTheDocument();
	});

	it('shows the Claude subscription setup when subscriptions are allowed', async () => {
		render(GatewayConnect, { obotURL, subscriptionPassthrough: true });

		await expect
			.element(page.getByText('With a Claude subscription (Pro, Max, Team or Enterprise)'))
			.toBeInTheDocument();
		await expect
			.element(
				page.getByText(
					'export ANTHROPIC_CUSTOM_HEADERS="X-Obot-Api-Key: $(obot login --url https://obot.example.com --scope llm --print-token)"',
					{ exact: false }
				)
			)
			.toBeInTheDocument();
		await expect
			.element(page.getByText('unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN', { exact: false }))
			.toBeInTheDocument();
		await expect.element(page.getByText('With an Obot API key')).toBeInTheDocument();
	});

	it('uses the first available model of each API format in the examples', async () => {
		render(GatewayConnect, { obotURL, models });

		await page.getByRole('tab', { name: 'Anthropic SDK' }).click();
		await expect
			.element(page.getByText('model="anthropic/claude-opus-4-5"', { exact: false }))
			.toBeInTheDocument();

		await page.getByRole('tab', { name: 'Codex CLI' }).click();
		await expect.element(page.getByText('~/.codex/config.toml')).toBeInTheDocument();
		await expect
			.element(page.getByText('model = "openai/gpt-4o"', { exact: false }))
			.toBeInTheDocument();
		await expect
			.element(
				page.getByText('base_url = "https://obot.example.com/api/llm-proxy/v1"', { exact: false })
			)
			.toBeInTheDocument();
	});

	it('switches between client snippets', async () => {
		render(GatewayConnect, { obotURL });

		await page.getByRole('tab', { name: 'curl' }).click();

		await expect
			.element(page.getByRole('tab', { name: 'curl' }))
			.toHaveAttribute('aria-selected', 'true');
		await expect
			.element(page.getByRole('tab', { name: 'Claude Code' }))
			.toHaveAttribute('aria-selected', 'false');
		await expect.element(page.getByText('OpenAI Chat Completions (any model)')).toBeInTheDocument();
		await expect.element(page.getByText('Anthropic Messages (any model)')).toBeInTheDocument();
		await expect
			.element(page.getByText('export ANTHROPIC_BASE_URL', { exact: false }))
			.not.toBeInTheDocument();
	});
});
