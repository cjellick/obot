import { page as appPage } from '$app/state';
import { preparePageData } from '../../tests/helpers/pageData';
import { worker } from '../../tests/mocks/worker';
import EventsView from './EventsView.svelte';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';
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

const range = {
	start_time: '2026-08-31T00:00:00.000Z',
	end_time: '2026-09-02T00:00:00.000Z'
};

function setParams(params: Record<string, string>) {
	for (const [k, v] of Object.entries(params)) appPage.url.searchParams.set(k, v);
}

function mockSupport() {
	worker.use(
		http.get('/api/users', () =>
			HttpResponse.json({ items: [{ id: 'u1', displayName: 'Ada Lovelace' }] })
		),
		http.get('/api/llm-gateway/guardrails/rules', () =>
			HttpResponse.json({ items: [{ id: 'r1', name: 'Block secrets' }] })
		)
	);
}

afterEach(() => {
	for (const key of [...appPage.url.searchParams.keys()]) appPage.url.searchParams.delete(key);
});

describe('Guardrail events view', () => {
	it('lists MCP and LLM events with user names, outcome pills and findings', async () => {
		setParams(range);
		mockSupport();
		worker.use(
			http.get('/api/llm-gateway/guardrails/events', () =>
				HttpResponse.json({ items: [blocked, failed], total: 2 })
			)
		);
		await preparePageData();
		render(EventsView);

		await expect.element(page.getByRole('cell', { name: 'Block secrets' })).toBeInTheDocument();
		await expect.element(page.getByText('Ada Lovelace')).toBeInTheDocument();
		await expect.element(page.getByText('AWS_ACCESS_KEY ×2')).toBeInTheDocument();
		await expect.element(page.getByText('Blocked', { exact: true })).toHaveClass(/text-error/);
		await expect.element(page.getByText('LLM · Input')).toBeInTheDocument();
		await expect.element(page.getByText('MCP · Output')).toBeInTheDocument();
		await expect.element(page.getByText('github/create_issue')).toBeInTheDocument();
		await expect.element(page.getByText('analyzer unreachable')).toBeInTheDocument();
	});

	it('maps URL filters and the time range onto the events query', async () => {
		setParams({ ...range, outcome: 'redacted', target: 'mcp', rule_id: 'r1' });
		mockSupport();
		const requested = vi.fn();
		worker.use(
			http.get('/api/llm-gateway/guardrails/events', ({ request }) => {
				requested(Object.fromEntries(new URL(request.url).searchParams));
				return HttpResponse.json({ items: [], total: 0 });
			})
		);
		await preparePageData();
		render(EventsView);

		await vi.waitFor(() =>
			expect(requested).toHaveBeenLastCalledWith({
				limit: '500',
				offset: '0',
				outcome: 'redacted',
				target: 'mcp',
				ruleID: 'r1',
				since: '2026-08-31T00:00:00.000Z'
			})
		);
		await expect.element(page.getByText('Redacted', { exact: true })).toBeInTheDocument();
		await expect.element(page.getByText('MCP', { exact: true })).toBeInTheDocument();
		await expect.element(page.getByText('Block secrets')).toBeInTheDocument();
	});

	it('drops events newer than the end of the range', async () => {
		setParams({ start_time: range.start_time, end_time: '2026-09-01T11:30:00.000Z' });
		mockSupport();
		worker.use(
			http.get('/api/llm-gateway/guardrails/events', () =>
				HttpResponse.json({ items: [blocked, failed], total: 2 })
			)
		);
		await preparePageData();
		render(EventsView);

		await expect.element(page.getByRole('cell', { name: 'Tool check' })).toBeInTheDocument();
		await expect.element(page.getByText('Block secrets')).not.toBeInTheDocument();
	});

	it('shows an empty state without events', async () => {
		setParams(range);
		mockSupport();
		worker.use(
			http.get('/api/llm-gateway/guardrails/events', () =>
				HttpResponse.json({ items: [], total: 0 })
			)
		);
		await preparePageData();
		render(EventsView);

		await expect
			.element(page.getByRole('heading', { name: 'No guardrail events' }))
			.toBeInTheDocument();
		await expect
			.element(page.getByText('No guardrails blocked, redacted or logged anything in this range.'))
			.toBeInTheDocument();
	});

	it('opens the filters drawer with outcome, target and rule filters', async () => {
		setParams(range);
		mockSupport();
		worker.use(
			http.get('/api/llm-gateway/guardrails/events', () =>
				HttpResponse.json({ items: [], total: 0 })
			)
		);
		await preparePageData();
		render(EventsView);

		await page.getByRole('button', { name: 'Filters' }).click();
		await expect.element(page.getByRole('heading', { name: 'Filters' })).toBeInTheDocument();
		await expect.element(page.getByText('By Outcome', { exact: true })).toBeInTheDocument();
		await expect.element(page.getByText('By Target', { exact: true })).toBeInTheDocument();
		await expect.element(page.getByText('By Rule', { exact: true })).toBeInTheDocument();

		await page.getByCSS('#filter-rule_id').click();
		await expect
			.element(page.getByRole('button', { name: 'Block secrets', exact: true }))
			.toBeInTheDocument();
	});
});
