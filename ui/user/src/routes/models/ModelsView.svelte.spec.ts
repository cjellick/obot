import type { Model } from '$lib/services';
import accessibleModels from '$lib/stores/accessibleModels.svelte';
import { preparePageData } from '../../tests/helpers/pageData';
import ModelsView from './ModelsView.svelte';
import { describe, expect, it } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

function model(overrides: Partial<Model>): Model {
	return {
		id: 'm',
		active: true,
		aliasAssigned: false,
		created: 0,
		modelProvider: 'llm-gateway-model-provider',
		modelProviderName: 'LLM Gateway',
		name: 'model',
		displayName: 'model',
		targetModel: 'model',
		usage: 'llm',
		...overrides
	};
}

const gatewayModels = [
	model({
		id: 'g1',
		name: 'openai-gpt-4o',
		targetModel: 'openai/gpt-4o',
		dialect: 'OpenAIResponses'
	}),
	model({
		id: 'g2',
		name: 'anthropic-claude-opus',
		targetModel: 'anthropic/claude-opus-4-5',
		dialect: 'AnthropicMessages'
	})
];

const legacyAnthropicModel = model({
	id: 'a1',
	modelProvider: 'anthropic-model-provider',
	modelProviderName: 'Anthropic',
	name: 'claude-sonnet-4-5',
	targetModel: 'claude-sonnet-4-5',
	dialect: 'AnthropicMessages'
});

async function renderModelsView(models: Model[]) {
	await preparePageData();
	// The store is a module singleton shared by every test in this file.
	accessibleModels.set(models);
	return render(ModelsView);
}

describe('ModelsView', () => {
	it('shows an LLM gateway section with its models and client setup', async () => {
		await renderModelsView(gatewayModels);

		await expect.element(page.getByRole('heading', { name: 'LLM Gateway' })).toBeInTheDocument();
		await expect
			.element(page.getByText('2 models across every configured provider', { exact: false }))
			.toBeInTheDocument();
		await expect.element(page.getByText('openai/gpt-4o', { exact: true })).toBeInTheDocument();
		// Client setup points at this server's gateway.
		await expect
			.element(page.getByText('export ANTHROPIC_BASE_URL=', { exact: false }))
			.toBeInTheDocument();
	});

	it('ignores models from providers other than the gateway', async () => {
		await renderModelsView([...gatewayModels, legacyAnthropicModel]);

		await expect
			.element(page.getByText('2 models across every configured provider', { exact: false }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('heading', { name: 'Anthropic', exact: true }))
			.not.toBeInTheDocument();
		await expect
			.element(page.getByText('3 models across every configured provider', { exact: false }))
			.not.toBeInTheDocument();
	});

	it('omits the LLM gateway section without gateway models', async () => {
		await renderModelsView([legacyAnthropicModel]);

		await expect
			.element(page.getByRole('heading', { name: 'LLM Gateway' }))
			.not.toBeInTheDocument();
		await expect.element(page.getByRole('tab', { name: 'Claude Code' })).not.toBeInTheDocument();
	});
});
