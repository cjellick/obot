import { page as appPage } from '$app/state';
import { preparePageData } from '../../tests/helpers/pageData';
import { worker } from '../../tests/mocks/worker';
import GatewayUsageView from './GatewayUsageView.svelte';
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
	total_requests: 1200,
	success_rate: 99.5,
	average_latency: 0.42,
	total_tokens: 50000,
	prompt_tokens: 30000,
	completion_tokens: 20000,
	total_cost: 12.5
};

function mockGateway({
	status = readyStatus,
	rankings = [
		{
			model: 'gpt-4o',
			provider: 'openai',
			total_requests: 900,
			success_rate: 98.7,
			total_tokens: 40000,
			total_cost: 9.25,
			avg_latency: 0.5
		}
	] as unknown[]
}: { status?: typeof readyStatus; rankings?: unknown[] } = {}) {
	const statsQueries = vi.fn();
	worker.use(
		http.get('/api/llm-gateway/status', () => HttpResponse.json(status)),
		http.get('/api/llm-gateway/admin/logs/stats', ({ request }) => {
			statsQueries(new URL(request.url).searchParams);
			return HttpResponse.json(stats);
		}),
		http.get('/api/llm-gateway/admin/logs/histogram', () =>
			HttpResponse.json({ buckets: [], bucket_size_seconds: 3600 })
		),
		http.get('/api/llm-gateway/admin/logs/histogram/cost/by-provider', () =>
			HttpResponse.json({
				buckets: [
					{ timestamp: new Date().toISOString(), by_provider: { openai: 9.25, anthropic: 3.25 } }
				],
				bucket_size_seconds: 3600
			})
		),
		http.get('/api/llm-gateway/admin/logs/rankings', () => HttpResponse.json({ rankings }))
	);
	return { statsQueries };
}

async function renderGatewayUsage() {
	await preparePageData();
	return render(GatewayUsageView);
}

describe('Gateway usage view', () => {
	it('shows the stats strip, cost by provider and top models', async () => {
		mockGateway();
		await renderGatewayUsage();

		await expect.element(page.getByText('Requests', { exact: true }).first()).toBeInTheDocument();
		await expect.element(page.getByText('99.5%')).toBeInTheDocument();
		await expect.element(page.getByText('420 ms')).toBeInTheDocument();
		await expect.element(page.getByText('$12.50')).toBeInTheDocument();
		await expect.element(page.getByText('$9.25').first()).toBeInTheDocument();
		await expect.element(page.getByText('$3.25')).toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: 'gpt-4o', exact: true }))
			.toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: '98.7%' })).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: '500 ms' })).toBeInTheDocument();
		await expect.element(page.getByText('No requests in this period.')).toBeInTheDocument();
	});

	it('only counts inference requests over the last 7 days by default', async () => {
		const { statsQueries } = mockGateway();
		await renderGatewayUsage();

		await vi.waitFor(() => expect(statsQueries).toHaveBeenCalledOnce());
		const query = statsQueries.mock.calls[0][0] as URLSearchParams;
		expect(query.get('objects')?.split(',')).toContain('chat_completion');
		expect(query.get('objects')?.split(',')).not.toContain('list_models');
		const days =
			(Date.parse(query.get('end_time')!) - Date.parse(query.get('start_time')!)) / 86400e3;
		expect(Math.round(days)).toBe(7);
	});

	it('uses the time range from the URL', async () => {
		appPage.url.searchParams.set('start_time', '2026-09-01T00:00:00.000Z');
		appPage.url.searchParams.set('end_time', '2026-09-02T00:00:00.000Z');
		const { statsQueries } = mockGateway();
		const screen = await renderGatewayUsage();

		await vi.waitFor(() => expect(statsQueries).toHaveBeenCalledOnce());
		const query = statsQueries.mock.calls[0][0] as URLSearchParams;
		expect(query.get('start_time')).toBe('2026-09-01T00:00:00.000Z');
		expect(query.get('end_time')).toBe('2026-09-02T00:00:00.000Z');

		screen.unmount();
		appPage.url.searchParams.delete('start_time');
		appPage.url.searchParams.delete('end_time');
	});

	it('shows the starting banner and no usage while the gateway is starting', async () => {
		const { statsQueries } = mockGateway({ status: { ...readyStatus, ready: false } });
		await renderGatewayUsage();

		await expect.element(page.getByText('The LLM gateway is starting…')).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: /gpt-4o/ })).not.toBeInTheDocument();
		expect(statsQueries).not.toHaveBeenCalled();
	});
	it('prompts to add a provider when none are configured', async () => {
		mockGateway({ status: { ...readyStatus, hasProviders: false }, rankings: [] });
		await renderGatewayUsage();

		await expect.element(page.getByRole('link', { name: 'Add a provider' })).toBeInTheDocument();
	});
});
