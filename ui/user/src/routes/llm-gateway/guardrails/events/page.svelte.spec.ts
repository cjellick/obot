import { preparePageData } from '../../../../tests/helpers/pageData';
import { worker } from '../../../../tests/mocks/worker';
import EventsPage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

const blocked = {
	id: 2,
	created: '2026-09-01T12:00:00Z',
	ruleID: 'r1',
	ruleName: 'Block secrets',
	profileID: 'p1',
	profileName: 'Secrets',
	profileType: 'secrets',
	target: 'llm',
	stage: 'input',
	outcome: 'blocked',
	userID: 'u1',
	model: 'openai/gpt-5',
	findings: [{ category: 'AWS_ACCESS_KEY', count: 2 }]
};

const failed = {
	id: 1,
	created: '2026-09-01T11:00:00Z',
	ruleID: 'r2',
	ruleName: 'Tool check',
	profileName: 'Presidio',
	target: 'mcp',
	stage: 'output',
	outcome: 'error',
	resource: 'github/create_issue',
	error: 'analyzer unreachable'
};

describe('Guardrail events page', () => {
	it('lists events with user names, outcomes and findings', async () => {
		worker.use(
			http.get('/api/llm-gateway/guardrails/events', () =>
				HttpResponse.json({ items: [blocked, failed], total: 2 })
			),
			http.get('/api/users', () =>
				HttpResponse.json({ items: [{ id: 'u1', displayName: 'Ada Lovelace' }] })
			)
		);
		await preparePageData();
		render(EventsPage);

		await expect.element(page.getByText('Block secrets')).toBeInTheDocument();
		await expect.element(page.getByText('Ada Lovelace')).toBeInTheDocument();
		await expect.element(page.getByText('AWS_ACCESS_KEY ×2')).toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: 'Blocked', exact: true }))
			.toBeInTheDocument();
		await expect.element(page.getByText('github/create_issue')).toBeInTheDocument();
		await expect.element(page.getByText('analyzer unreachable')).toBeInTheDocument();
		await expect.element(page.getByText('1–2 of 2')).toBeInTheDocument();
	});

	it('filters by outcome and target', async () => {
		const requested = vi.fn();
		worker.use(
			http.get('/api/llm-gateway/guardrails/events', ({ request }) => {
				requested(new URL(request.url).searchParams.toString());
				return HttpResponse.json({ items: [], total: 0 });
			})
		);
		await preparePageData();
		render(EventsPage);

		await expect.element(page.getByText('No guardrail events match.')).toBeInTheDocument();
		await page.getByLabelText('Outcome').selectOptions('redacted');
		await page.getByLabelText('Target').selectOptions('mcp');

		await vi.waitFor(() =>
			expect(requested).toHaveBeenLastCalledWith('limit=50&offset=0&outcome=redacted&target=mcp')
		);
	});
});
