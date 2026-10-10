import { Group } from '$lib/services';
import { goto } from '$lib/url';
import { createMockProfile, preparePageData } from '../../../tests/helpers/pageData';
import { worker } from '../../../tests/mocks/worker';
import BudgetsPage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

vi.mock(import('$lib/url'), async (importOriginal) => ({
	...(await importOriginal()),
	goto: vi.fn()
}));

const engineering = {
	id: 'b1',
	name: 'Engineering',
	enabled: true,
	perUser: true,
	exemptAdmins: false,
	limits: [{ metric: 'spend', amount: 25, window: '1M', calendar: true }],
	subjects: [{ type: 'group', id: 'engineers' }]
};

const sales = {
	id: 'b2',
	name: 'Sales',
	enabled: false,
	perUser: false,
	exemptAdmins: false,
	limits: [{ metric: 'tokens', amount: 1000, window: '6h' }],
	models: ['m1', 'm2'],
	subjects: [{ type: 'everyone' }]
};

function mockBudgets(budgets: unknown[] = [engineering, sales]) {
	const listed = vi.fn();
	worker.use(
		http.get('/api/llm-gateway/budgets', () => {
			listed();
			return HttpResponse.json({ items: budgets });
		})
	);
	return { listed };
}

async function renderBudgetsPage(groups: string[] = [Group.ADMIN]) {
	await preparePageData({ profile: createMockProfile(groups) });
	return render(BudgetsPage);
}

describe('LLM gateway budgets page', () => {
	it('lists budgets with their assignment, allowance and limits', async () => {
		mockBudgets();
		await renderBudgetsPage();

		await expect
			.element(page.getByRole('cell', { name: 'Engineering', exact: true }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: 'Group: engineers', exact: true }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: 'Each user', exact: true }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: '$25.00 spend (usd) per calendar month' }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: 'Shared', exact: true }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: '1K total tokens per rolling 6 hours' }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: '2 models', exact: true }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: 'Disabled', exact: true }))
			.toBeInTheDocument();
	});

	it('shows an empty state without budgets', async () => {
		mockBudgets([]);
		await renderBudgetsPage();

		await expect.element(page.getByRole('heading', { name: 'No budgets' })).toBeInTheDocument();
	});

	it('opens the new budget form', async () => {
		mockBudgets();
		await renderBudgetsPage();

		await page.getByRole('button', { name: 'Add Budget' }).click();

		expect(goto).toHaveBeenCalledWith('/llm-gateway/budgets/new');
	});

	it('deletes a budget after confirmation and reloads the list', async () => {
		const { listed } = mockBudgets();
		const deleted = vi.fn();
		worker.use(
			http.delete('/api/llm-gateway/budgets/:id', ({ params }) => {
				deleted(params.id);
				return new HttpResponse(null, { status: 204 });
			})
		);
		await renderBudgetsPage();

		await page.getByRole('button', { name: 'Delete budget' }).last().click();
		await expect
			.element(page.getByText('Delete Sales? Its limits stop applying immediately.'))
			.toBeInTheDocument();
		await page.getByRole('button', { name: "Yes, I'm sure" }).click();

		await vi.waitFor(() => expect(deleted).toHaveBeenCalledWith('b2'));
		expect(deleted).toHaveBeenCalledOnce();
		await vi.waitFor(() => expect(listed).toHaveBeenCalledTimes(2));
	});

	it('does not delete a budget when the confirmation is cancelled', async () => {
		mockBudgets();
		const deleted = vi.fn();
		worker.use(
			http.delete('/api/llm-gateway/budgets/:id', () => {
				deleted();
				return new HttpResponse(null, { status: 204 });
			})
		);
		await renderBudgetsPage();

		await page.getByRole('button', { name: 'Delete budget' }).first().click();
		await page.getByRole('button', { name: 'Cancel' }).click();

		await expect
			.element(page.getByText('Delete Engineering? Its limits stop applying immediately.'))
			.not.toBeInTheDocument();
		expect(deleted).not.toHaveBeenCalled();
	});

	it('hides management actions from auditors', async () => {
		mockBudgets();
		await renderBudgetsPage([Group.USER, Group.AUDITOR]);

		await expect
			.element(page.getByRole('cell', { name: 'Engineering', exact: true }))
			.toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Add Budget' })).not.toBeInTheDocument();
		await expect
			.element(page.getByRole('button', { name: 'Delete budget' }))
			.not.toBeInTheDocument();
	});
});
