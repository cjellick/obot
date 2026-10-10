import { Group } from '$lib/services';
import { goto } from '$lib/url';
import { worker } from '../../tests/mocks/worker';
import { readyStatus, renderModelsTab, resetModelsTab } from './testing';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { page } from 'vitest/browser';

vi.mock(import('$lib/url'), async (importOriginal) => ({
	...(await importOriginal()),
	goto: vi.fn()
}));

const openai = {
	name: 'openai',
	network_config: { base_url: '', max_retries: 2 },
	concurrency_and_buffer_size: { concurrency: 1000, buffer_size: 5000 },
	provider_status: 'active'
};

function mockGateway({
	status = readyStatus,
	providers = [openai],
	keys = [{ id: 'k1', name: 'openai-key-1', value: { value: 'sk-***' }, models: ['*'], weight: 1 }]
}: {
	status?: typeof readyStatus;
	providers?: (typeof openai)[];
	keys?: unknown[];
} = {}) {
	worker.use(
		http.get('/api/llm-gateway/status', () => HttpResponse.json(status)),
		http.get('/api/llm-gateway/admin/providers', () =>
			HttpResponse.json({ providers, total: providers.length })
		),
		http.get('/api/llm-gateway/admin/providers/:name/keys', () =>
			HttpResponse.json({ keys, total: keys.length })
		)
	);
}

async function renderProvidersPage(groups: string[] = [Group.ADMIN]) {
	return renderModelsTab('providers', groups);
}

afterEach(resetModelsTab);

describe('Models providers tab', () => {
	it('lists gateway providers as provider cards with their key counts', async () => {
		mockGateway();
		await renderProvidersPage();

		await expect.element(page.getByRole('heading', { name: 'OpenAI' })).toBeInTheDocument();
		await expect.element(page.getByText('1 key', { exact: true })).toBeInTheDocument();
		await expect.element(page.getByText('Configured', { exact: true })).toBeInTheDocument();
	});

	it('opens a provider from its card', async () => {
		mockGateway();
		await renderProvidersPage();

		await page.getByRole('button', { name: 'Modify' }).click();

		expect(goto).toHaveBeenCalledWith('/models/providers/openai');
	});

	it('shows the legacy providers in their own section', async () => {
		mockGateway();
		await renderProvidersPage();

		await expect
			.element(page.getByRole('heading', { name: 'Legacy providers' }))
			.toBeInTheDocument();
		await expect
			.element(
				page.getByText('Per-provider integrations Obot used before the gateway', { exact: false })
			)
			.toBeInTheDocument();
	});

	it('shows an empty state when no providers are configured', async () => {
		mockGateway({ providers: [] });
		await renderProvidersPage();

		await expect.element(page.getByRole('heading', { name: 'No providers' })).toBeInTheDocument();
	});

	it('shows a starting banner and no actions while the gateway is starting', async () => {
		mockGateway({ status: { ...readyStatus, ready: false } });
		await renderProvidersPage();

		await expect.element(page.getByText('The LLM gateway is starting…')).toBeInTheDocument();
		await expect
			.element(page.getByRole('button', { name: 'Add Provider' }))
			.not.toBeInTheDocument();
	});

	it('hides management actions from auditors', async () => {
		mockGateway();
		await renderProvidersPage([Group.AUDITOR]);

		await expect.element(page.getByRole('heading', { name: 'OpenAI' })).toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'View' })).toBeInTheDocument();
		await expect
			.element(page.getByRole('button', { name: 'Add Provider' }))
			.not.toBeInTheDocument();
		await expect
			.element(page.getByRole('button', { name: /Import legacy providers/ }))
			.not.toBeInTheDocument();
	});

	it('imports legacy providers and reports the results', async () => {
		mockGateway();
		const importLegacy = vi.fn();
		worker.use(
			http.post('/api/llm-gateway/import-legacy-providers', () => {
				importLegacy();
				return HttpResponse.json({
					items: [
						{
							modelProvider: 'anthropic-model-provider',
							gatewayProvider: 'anthropic',
							status: 'imported'
						}
					]
				});
			})
		);
		await renderProvidersPage();

		await page.getByRole('button', { name: /Import legacy providers/ }).click();

		await vi.waitFor(() => expect(importLegacy).toHaveBeenCalledOnce());
		await expect.element(page.getByText('anthropic-model-provider')).toBeInTheDocument();
		await expect.element(page.getByText('imported', { exact: true })).toBeInTheDocument();
	});

	it('adds a provider with its first key', async () => {
		mockGateway({ providers: [] });
		const created = vi.fn();
		const keyCreated = vi.fn();
		worker.use(
			http.post('/api/llm-gateway/admin/providers', async ({ request }) => {
				created(await request.json());
				return HttpResponse.json({ ...openai, name: 'groq' });
			}),
			http.post('/api/llm-gateway/admin/providers/groq/keys', async ({ request }) => {
				keyCreated(await request.json());
				return HttpResponse.json({ id: 'k2', name: 'groq-key-1' });
			})
		);
		await renderProvidersPage();

		await page.getByRole('button', { name: 'Add Provider' }).first().click();
		await page.getByRole('button', { name: /Groq/ }).click();
		await page.getByPlaceholder('gsk_...').fill('gsk_secret');
		await page.getByRole('button', { name: 'Add Provider' }).last().click();

		await vi.waitFor(() => expect(keyCreated).toHaveBeenCalledOnce());
		expect(created).toHaveBeenCalledWith(expect.objectContaining({ provider: 'groq' }));
		expect(keyCreated).toHaveBeenCalledWith(
			expect.objectContaining({ name: 'groq-key-1', value: 'gsk_secret', models: ['*'] })
		);
		await vi.waitFor(() => expect(goto).toHaveBeenCalledWith('/models/providers/groq'));
	});
});
