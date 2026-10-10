import { preparePageData } from '../../../tests/helpers/pageData';
import { worker } from '../../../tests/mocks/worker';
import OverviewPage from './+page.svelte';
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

async function renderOverviewPage() {
	await preparePageData();
	return render(OverviewPage);
}

describe('LLM gateway overview page', () => {
	it('shows cost by provider, top models and client setup', async () => {
		mockGateway();
		await renderOverviewPage();

		await expect.element(page.getByText('$9.25').first()).toBeInTheDocument();
		await expect.element(page.getByText('$3.25')).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: /gpt-4o/ })).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: '98.7%' })).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: '500 ms' })).toBeInTheDocument();
		await expect.element(page.getByText('No requests in this period.')).toBeInTheDocument();
		await expect.element(page.getByRole('tab', { name: 'Claude Code' })).toBeInTheDocument();
	});

	it('only counts inference requests and reloads for a new time range', async () => {
		const { statsQueries } = mockGateway();
		await renderOverviewPage();

		await vi.waitFor(() => expect(statsQueries).toHaveBeenCalledOnce());
		const first = statsQueries.mock.calls[0][0] as URLSearchParams;
		expect(first.get('objects')?.split(',')).toContain('chat_completion');
		expect(first.get('objects')?.split(',')).not.toContain('list_models');

		await page.getByLabelText('Time range').selectOptions('7d');

		await vi.waitFor(() => expect(statsQueries).toHaveBeenCalledTimes(2));
		const second = statsQueries.mock.calls[1][0] as URLSearchParams;
		const days =
			(Date.parse(second.get('end_time')!) - Date.parse(second.get('start_time')!)) / 86400e3;
		expect(Math.round(days)).toBe(7);
	});

	it('shows the starting banner and no usage while the gateway is starting', async () => {
		const { statsQueries } = mockGateway({ status: { ...readyStatus, ready: false } });
		await renderOverviewPage();

		await expect.element(page.getByText('The LLM gateway is starting…')).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: /gpt-4o/ })).not.toBeInTheDocument();
		expect(statsQueries).not.toHaveBeenCalled();
	});

	it('shows the disabled banner when the gateway is turned off', async () => {
		mockGateway({ status: { ...readyStatus, enabled: false, ready: false } });
		await renderOverviewPage();

		await expect
			.element(page.getByText('The LLM gateway is disabled on this Obot server', { exact: false }))
			.toBeInTheDocument();
	});

	it('prompts to add a provider when none are configured', async () => {
		mockGateway({ status: { ...readyStatus, hasProviders: false }, rankings: [] });
		await renderOverviewPage();

		await expect.element(page.getByRole('link', { name: 'Add a provider' })).toBeInTheDocument();
	});
});
