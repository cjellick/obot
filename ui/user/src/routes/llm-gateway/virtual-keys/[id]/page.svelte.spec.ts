import { page as appPage } from '$app/state';
import { goto } from '$lib/url';
import { preparePageData } from '../../../../tests/helpers/pageData';
import { worker } from '../../../../tests/mocks/worker';
import BudgetPage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

vi.mock(import('$lib/url'), async (importOriginal) => ({
	...(await importOriginal()),
	goto: vi.fn()
}));

function mockProviders() {
	worker.use(
		http.get('/api/llm-gateway/bifrost/providers', () =>
			HttpResponse.json({
				providers: [{ name: 'openai', network_config: {}, concurrency_and_buffer_size: {} }]
			})
		)
	);
}

afterEach(() => {
	delete (appPage.params as Record<string, string>).id;
});

describe('LLM gateway budget page', () => {
	it('creates a budget with a spend limit and rate limit', async () => {
		mockProviders();
		const created = vi.fn();
		const bound = vi.fn();
		worker.use(
			http.post('/api/llm-gateway/bifrost/governance/virtual-keys', async ({ request }) => {
				created(await request.json());
				return HttpResponse.json({ virtual_key: { id: 'vk-1', name: 'Engineering' } });
			}),
			http.put('/api/llm-gateway/budget-bindings/vk-1', async ({ request }) => {
				bound(await request.json());
				return HttpResponse.json({ items: [] });
			})
		);
		await preparePageData();
		render(BudgetPage);

		await page.getByLabelText('Name').first().fill('Engineering');
		await page.getByRole('button', { name: 'Add budget' }).click();
		await page.getByLabelText('Limit (USD)').fill('25');
		await page.getByLabelText('Max requests').fill('100');
		await page.getByRole('button', { name: 'Save', exact: true }).click();

		await vi.waitFor(() => expect(bound).toHaveBeenCalledOnce());
		expect(created).toHaveBeenCalledWith(
			expect.objectContaining({
				name: 'Engineering',
				allow_all_providers: true,
				budgets: [{ max_limit: 25, reset_duration: '1M' }],
				rate_limit: expect.objectContaining({
					request_max_limit: 100,
					request_reset_duration: '1h'
				})
			})
		);
		expect(bound).toHaveBeenCalledWith({ subjects: [] });
		expect(goto).toHaveBeenCalledWith('/llm-gateway/virtual-keys');
	});

	it('loads an existing budget with its usage and assignments', async () => {
		mockProviders();
		(appPage.params as Record<string, string>).id = 'vk-2';
		worker.use(
			http.get('/api/llm-gateway/bifrost/governance/virtual-keys/vk-2', () =>
				HttpResponse.json({
					virtual_key: {
						id: 'vk-2',
						name: 'Sales',
						is_active: true,
						allow_all_providers: false,
						provider_configs: [{ id: 1, provider: 'openai', allowed_models: ['gpt-4.1'] }],
						budgets: [{ max_limit: 50, reset_duration: '1w', current_usage: 12.5 }]
					}
				})
			),
			http.get('/api/llm-gateway/budget-bindings', () =>
				HttpResponse.json({ items: [{ virtualKeyID: 'vk-2', subjectType: 'everyone' }] })
			)
		);
		await preparePageData();
		render(BudgetPage);

		await expect.element(page.getByLabelText('Name').first()).toHaveValue('Sales');
		await expect.element(page.getByText('$12.50 of $50.00')).toBeInTheDocument();
		await expect.element(page.getByText('All Obot Users')).toBeInTheDocument();
		await expect.element(page.getByLabelText('Allowed models')).toHaveValue('gpt-4.1');
	});
});
