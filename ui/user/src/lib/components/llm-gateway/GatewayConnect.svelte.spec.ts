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
		// Falls back to a placeholder model without any available models.
		await expect
			.element(page.getByText('ANTHROPIC_MODEL="anthropic/claude-sonnet-4-5"', { exact: false }))
			.toBeInTheDocument();
	});

	it('uses the first available model of each API format in the examples', async () => {
		render(GatewayConnect, { obotURL, models });

		await expect
			.element(page.getByText('ANTHROPIC_MODEL="anthropic/claude-opus-4-5"', { exact: false }))
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
