import { Group } from '$lib/services';
import { goto } from '$lib/url';
import { worker } from '../../tests/mocks/worker';
import { renderModelsTab, resetModelsTab } from './testing';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { page } from 'vitest/browser';

vi.mock(import('$lib/url'), async (importOriginal) => ({
	...(await importOriginal()),
	goto: vi.fn()
}));

const gatewayTabs = [
	'Providers',
	'Routing',
	'Budgets & Limits',
	'Guardrails',
	'Access Policies',
	'Settings'
];

function mockBudgets() {
	worker.use(http.get('/api/llm-gateway/budgets', () => HttpResponse.json({ items: [] })));
}

afterEach(resetModelsTab);

describe('Models page', () => {
	it('shows admins the Models tab and every gateway tab', async () => {
		await renderModelsTab('models');

		await expect.element(page.getByRole('button', { name: 'Models', exact: true })).toBeVisible();
		for (const tab of gatewayTabs) {
			await expect.element(page.getByRole('button', { name: tab, exact: true })).toBeVisible();
		}
	});

	it('shows users only their models, without gateway tabs', async () => {
		await renderModelsTab('models', [Group.USER]);

		for (const tab of gatewayTabs) {
			await expect
				.element(page.getByRole('button', { name: tab, exact: true }))
				.not.toBeInTheDocument();
		}
	});

	it('does not show intro copy on the Models tab', async () => {
		await renderModelsTab('models');

		await expect
			.element(page.getByText('Use the Obot LLM Gateway to call', { exact: false }))
			.not.toBeInTheDocument();
	});

	it('offers Add Budget on the budgets tab', async () => {
		mockBudgets();
		await renderModelsTab('budgets');

		await page.getByRole('button', { name: 'Add Budget' }).click();

		expect(goto).toHaveBeenCalledWith('/models/budgets/new');
	});

	it('offers Add Access Policy on the access policies tab', async () => {
		await renderModelsTab('access-policies');

		await page.getByRole('button', { name: 'Add Access Policy' }).first().click();

		expect(goto).toHaveBeenCalledWith(expect.stringContaining('?view=access-policies&new=true'));
	});

	it('links the guardrails tab to the detectors', async () => {
		worker.use(
			http.get('/api/llm-gateway/guardrails/rules', () => HttpResponse.json({ items: [] })),
			http.get('/api/llm-gateway/guardrails/profiles', () => HttpResponse.json({ items: [] }))
		);
		await renderModelsTab('guardrails', [Group.USER, Group.AUDITOR]);

		// Read-only admins can view the detectors, but not add rules.
		await expect.element(page.getByRole('button', { name: 'Add Rule' })).not.toBeInTheDocument();
		await page.getByRole('button', { name: 'Manage Detectors' }).click();

		expect(goto).toHaveBeenCalledWith('/guardrails');
	});

	it('hides create actions from read-only admins', async () => {
		mockBudgets();
		await renderModelsTab('budgets', [Group.USER, Group.AUDITOR]);

		await expect.element(page.getByRole('heading', { name: 'No budgets' })).toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Add Budget' })).not.toBeInTheDocument();
	});
});
