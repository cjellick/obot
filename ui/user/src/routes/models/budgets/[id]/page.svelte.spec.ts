import { page as appPage } from '$app/state';
import { Group } from '$lib/services';
import { goto } from '$lib/url';
import { createMockProfile, preparePageData } from '../../../../tests/helpers/pageData';
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

function mockModels() {
	worker.use(
		http.get('/api/models', () =>
			HttpResponse.json({
				items: [
					{
						id: 'm1-opus',
						name: 'anthropic-claude-opus',
						targetModel: 'anthropic/claude-opus-4-5',
						modelProvider: 'llm-gateway-model-provider'
					}
				]
			})
		)
	);
}

afterEach(() => {
	delete (appPage.params as Record<string, string>).id;
});

describe('Models budget editor', () => {
	it('creates a per-user budget with a monthly spend limit by default', async () => {
		mockModels();
		const created = vi.fn();
		worker.use(
			http.post('/api/llm-gateway/budgets', async ({ request }) => {
				created(await request.json());
				return HttpResponse.json({ id: 'b1' });
			})
		);
		await preparePageData();
		render(BudgetPage);

		await page.getByLabelText('Name').first().fill('Engineering');
		await page.getByLabelText('Limit', { exact: true }).fill('25');
		// The model list loads asynchronously.
		await expect.element(page.getByText('anthropic/claude-opus-4-5')).toBeVisible();
		await page.getByText('anthropic/claude-opus-4-5').click();
		await page.getByRole('button', { name: 'Save', exact: true }).click();

		await vi.waitFor(() => expect(created).toHaveBeenCalledOnce());
		expect(created).toHaveBeenCalledWith(
			expect.objectContaining({
				name: 'Engineering',
				enabled: true,
				perUser: true,
				limits: [{ metric: 'spend', amount: 25, window: '1M', calendar: true }],
				models: ['m1-opus'],
				subjects: []
			})
		);
		await vi.waitFor(() => expect(goto).toHaveBeenCalledWith('/models?view=budgets'));
	});

	it('shows a shared budget with its assignments and current usage', async () => {
		mockModels();
		(appPage.params as Record<string, string>).id = 'b2';
		worker.use(
			http.get('/api/llm-gateway/budgets/b2', () =>
				HttpResponse.json({
					id: 'b2',
					name: 'Sales',
					enabled: true,
					perUser: false,
					exemptAdmins: false,
					limits: [{ metric: 'tokens', amount: 1000, window: '6h' }],
					subjects: [{ type: 'everyone' }]
				})
			),
			http.get('/api/llm-gateway/budgets/b2/usage', () =>
				HttpResponse.json({ items: [{ used: [250] }] })
			)
		);
		await preparePageData();
		render(BudgetPage);

		await expect.element(page.getByLabelText('Name').first()).toHaveValue('Sales');
		await expect.element(page.getByText('All Obot Users')).toBeInTheDocument();
		await expect.element(page.getByText('Everyone (shared)')).toBeInTheDocument();
		await expect.element(page.getByText('250 / 1K')).toBeInTheDocument();
		await expect.element(page.getByLabelText('Window length')).toHaveValue(6);
	});

	it('saves a rolling token limit chosen with the dropdowns', async () => {
		mockModels();
		const created = vi.fn();
		worker.use(
			http.post('/api/llm-gateway/budgets', async ({ request }) => {
				created(await request.json());
				return HttpResponse.json({ id: 'b1' });
			})
		);
		await preparePageData();
		render(BudgetPage);

		await page.getByLabelText('Name').first().fill('Tokens');
		await page.getByRole('combobox', { name: 'Metric' }).click();
		await page.getByRole('button', { name: 'Total tokens', exact: true }).click();
		await page.getByLabelText('Limit', { exact: true }).fill('5000');
		await page.getByRole('combobox', { name: 'Window type' }).click();
		await page.getByRole('button', { name: 'Rolling', exact: true }).click();
		await page.getByLabelText('Window length').fill('2');
		await page.getByRole('combobox', { name: 'Window unit' }).click();
		await page.getByRole('button', { name: 'days', exact: true }).click();
		await page.getByRole('button', { name: 'Save', exact: true }).click();

		await vi.waitFor(() => expect(created).toHaveBeenCalledOnce());
		expect(created).toHaveBeenCalledWith(
			expect.objectContaining({
				limits: [{ metric: 'tokens', amount: 5000, window: '2d', calendar: false }]
			})
		);
	});

	it('hides editing actions from auditors', async () => {
		mockModels();
		await preparePageData({ profile: createMockProfile([Group.USER, Group.AUDITOR]) });
		render(BudgetPage);

		await expect.element(page.getByLabelText('Name').first()).toBeDisabled();
		await expect
			.element(page.getByRole('button', { name: 'Save', exact: true }))
			.not.toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Add limit' })).not.toBeInTheDocument();
	});
});
