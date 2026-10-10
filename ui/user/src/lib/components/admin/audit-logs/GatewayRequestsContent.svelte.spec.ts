import { page as appPage } from '$app/state';
import { preparePageData } from '../../../../tests/helpers/pageData';
import { worker } from '../../../../tests/mocks/worker';
import GatewayRequestsContent from './GatewayRequestsContent.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

const readyStatus = {
	enabled: true,
	ready: true,
	hasProviders: true,
	modelProviderName: 'llm-gateway-model-provider',
	proxyPath: '/api/llm-proxy'
};

const stats = {
	total_requests: 2,
	success_rate: 50,
	average_latency: 0.3,
	total_tokens: 150,
	prompt_tokens: 100,
	completion_tokens: 50,
	total_cost: 0.5
};

// Rows render in the audit logs page's virtualized viewport, so listing and opening requests is
// covered by routes/audit-logs/page.svelte.spec.ts. These cases cover the query and empty states.
function mockGateway({
	status = readyStatus,
	logs = [] as unknown[]
}: { status?: typeof readyStatus; logs?: unknown[] } = {}) {
	const logQueries = vi.fn();
	worker.use(
		http.get('/api/llm-gateway/status', () => HttpResponse.json(status)),
		http.get('/api/llm-gateway/admin/logs', ({ request }) => {
			logQueries(new URL(request.url).searchParams);
			return HttpResponse.json({ logs, pagination: { total_count: logs.length }, stats });
		}),
		http.get('/api/llm-gateway/admin/logs/stats', () => HttpResponse.json(stats)),
		http.get('/api/llm-gateway/admin/logs/filterdata', () =>
			HttpResponse.json({
				models: ['gpt-4o', 'claude-sonnet-4-5'],
				metadata_keys: { 'obot-user-name': ['alice', 'bob'] }
			})
		),
		http.get('/api/llm-gateway/admin/providers', () =>
			HttpResponse.json({
				providers: [{ name: 'openai' }, { name: 'anthropic' }],
				total: 2
			})
		)
	);
	return { logQueries };
}

async function renderGatewayRequests() {
	await preparePageData();
	return render(GatewayRequestsContent);
}

async function firstQuery(logQueries: ReturnType<typeof vi.fn>) {
	await vi.waitFor(() => expect(logQueries).toHaveBeenCalled());
	return logQueries.mock.calls[0][0] as URLSearchParams;
}

const filterParams = ['provider', 'model', 'status', 'user', 'hide_models_requests', 'query'];

function clearFilterParams(screen: { unmount: () => void }) {
	screen.unmount();
	for (const key of filterParams) appPage.url.searchParams.delete(key);
}

describe('GatewayRequestsContent', () => {
	it('hides model discovery requests by default and covers the last 7 days', async () => {
		const { logQueries } = mockGateway();
		await renderGatewayRequests();

		const query = await firstQuery(logQueries);
		expect(query.get('objects')?.split(',')).toContain('chat_completion');
		expect(query.get('objects')?.split(',')).not.toContain('list_models');
		expect(query.get('offset')).toBe('0');
		const days =
			(Date.parse(query.get('end_time')!) - Date.parse(query.get('start_time')!)) / 86400e3;
		expect(Math.round(days)).toBe(7);
		await expect
			.element(page.getByCSS('.filter-primary').filter({ hasText: 'Model discovery requests' }))
			.toBeVisible();
	});

	it('maps URL filters onto the gateway log query', async () => {
		appPage.url.searchParams.set('provider', 'openai,anthropic');
		appPage.url.searchParams.set('model', 'gpt-4o');
		appPage.url.searchParams.set('status', 'error');
		appPage.url.searchParams.set('user', 'bob');
		appPage.url.searchParams.set('query', 'refund');
		const { logQueries } = mockGateway();
		const screen = await renderGatewayRequests();

		const query = await firstQuery(logQueries);
		expect(query.get('providers')).toBe('openai,anthropic');
		expect(query.get('models')).toBe('gpt-4o');
		expect(query.get('status')).toBe('error');
		expect(query.get('metadata_obot-user-name')).toBe('bob');
		expect(query.get('content_search')).toBe('refund');
		await expect
			.element(page.getByCSS('.filter-primary').filter({ hasText: 'User' }))
			.toBeVisible();
		clearFilterParams(screen);
	});

	it('includes model discovery requests when the default filter is cleared', async () => {
		appPage.url.searchParams.set('hide_models_requests', '');
		const { logQueries } = mockGateway();
		const screen = await renderGatewayRequests();

		const query = await firstQuery(logQueries);
		expect(query.has('objects')).toBe(false);
		await expect
			.element(page.getByCSS('.filter-primary').filter({ hasText: 'Model discovery requests' }))
			.not.toBeInTheDocument();
		clearFilterParams(screen);
	});

	it('shows an empty state when nothing matches', async () => {
		mockGateway();
		await renderGatewayRequests();

		await expect
			.element(page.getByRole('heading', { name: 'No gateway requests' }))
			.toBeInTheDocument();
	});

	it('opens the filters drawer with the gateway filters', async () => {
		mockGateway();
		await renderGatewayRequests();

		await page.getByRole('button', { name: 'Filters' }).click();

		await expect.element(page.getByRole('heading', { name: 'Filters' })).toBeVisible();
		for (const label of ['Provider', 'Model', 'Status', 'User', 'Model discovery requests']) {
			await expect.element(page.getByText(`By ${label}`, { exact: true })).toBeVisible();
		}
	});

	it('shows the starting banner and does not query while the gateway is starting', async () => {
		const { logQueries } = mockGateway({ status: { ...readyStatus, ready: false } });
		await renderGatewayRequests();

		await expect.element(page.getByText('The LLM gateway is starting…')).toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Filters' })).not.toBeInTheDocument();
		expect(logQueries).not.toHaveBeenCalled();
	});
});
