import { preparePageData } from '../../../tests/helpers/pageData';
import { worker } from '../../../tests/mocks/worker';
import LogsPage from './+page.svelte';
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

const successLog = {
	id: 'log-1',
	timestamp: '2026-10-01T12:00:00Z',
	object: 'chat_completion',
	provider: 'openai',
	model: 'gpt-4o',
	status: 'success',
	stream: true,
	latency: 0.25,
	cost: 0.5,
	number_of_retries: 1,
	selected_key_name: 'openai-key-1',
	token_usage: { prompt_tokens: 100, completion_tokens: 50, total_tokens: 150 },
	metadata: { 'obot-user-name': 'alice' }
};

const errorLog = {
	id: 'log-2',
	timestamp: '2026-10-01T12:01:00Z',
	object: 'chat_completion',
	provider: 'anthropic',
	model: 'claude-sonnet-4-5',
	status: 'error',
	stream: false,
	fallback_index: 1,
	metadata: { 'obot-user-name': 'bob' }
};

function mockGateway({
	status = readyStatus,
	logs = [successLog, errorLog] as unknown[]
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
		),
		http.get('/api/llm-gateway/admin/logs/:id', ({ params }) =>
			HttpResponse.json(
				params.id === 'log-2'
					? {
							...errorLog,
							error_details: { error: { message: 'upstream overloaded' } },
							routing_rule_name: 'Cheaper GPT'
						}
					: successLog
			)
		)
	);
	return { logQueries };
}

async function renderLogsPage() {
	await preparePageData();
	return render(LogsPage);
}

describe('LLM gateway request logs page', () => {
	it('lists requests with their user, key, retries and fallbacks', async () => {
		mockGateway();
		await renderLogsPage();

		await expect.element(page.getByRole('cell', { name: 'alice' })).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: 'openai-key-1' })).toBeInTheDocument();
		await expect.element(page.getByText('1 retries')).toBeInTheDocument();
		await expect.element(page.getByText('fallback', { exact: true })).toBeInTheDocument();
		await expect.element(page.getByText('chat_completion · stream')).toBeInTheDocument();
		await expect.element(page.getByText('1–2 of 2')).toBeInTheDocument();
	});

	it('shows request details with the error and routing rule', async () => {
		mockGateway();
		await renderLogsPage();

		await page.getByRole('cell', { name: 'bob' }).click();

		const details = page.getByRole('dialog', { name: 'Request details' });
		await expect.element(details.getByText('upstream overloaded')).toBeInTheDocument();
		await expect.element(details.getByText('Cheaper GPT')).toBeInTheDocument();
		await expect.element(details.getByText('#1')).toBeInTheDocument();

		await details.getByRole('button', { name: 'Close' }).click();
		await expect.element(details).not.toBeInTheDocument();
	});

	it('filters requests by status and user', async () => {
		const { logQueries } = mockGateway();
		await renderLogsPage();

		await vi.waitFor(() => expect(logQueries).toHaveBeenCalledOnce());
		const initial = logQueries.mock.calls[0][0] as URLSearchParams;
		expect(initial.get('objects')?.split(',')).toContain('chat_completion');
		expect(initial.get('offset')).toBe('0');

		await page.getByLabelText('Status').selectOptions('error');
		await vi.waitFor(() => expect(logQueries).toHaveBeenCalledTimes(2));
		expect((logQueries.mock.calls[1][0] as URLSearchParams).get('status')).toBe('error');

		await expect.element(page.getByRole('option', { name: 'bob' })).toBeInTheDocument();
		await page.getByLabelText('User').selectOptions('bob');
		await vi.waitFor(() => expect(logQueries).toHaveBeenCalledTimes(3));
		const byUser = logQueries.mock.calls[2][0] as URLSearchParams;
		expect(byUser.get('metadata_obot-user-name')).toBe('bob');
		expect(byUser.get('status')).toBe('error');
	});

	it('includes model listing requests only when asked', async () => {
		const { logQueries } = mockGateway();
		await renderLogsPage();

		await vi.waitFor(() => expect(logQueries).toHaveBeenCalledOnce());
		await page.getByLabelText('Include model listing').click();

		await vi.waitFor(() => expect(logQueries).toHaveBeenCalledTimes(2));
		expect((logQueries.mock.calls[1][0] as URLSearchParams).has('objects')).toBe(false);
	});

	it('shows an empty state when nothing matches', async () => {
		mockGateway({ logs: [] });
		await renderLogsPage();

		await expect.element(page.getByText('No requests match.')).toBeInTheDocument();
	});

	it('shows the starting banner and no requests while the gateway is starting', async () => {
		const { logQueries } = mockGateway({ status: { ...readyStatus, ready: false } });
		await renderLogsPage();

		await expect.element(page.getByText('The LLM gateway is starting…')).toBeInTheDocument();
		expect(logQueries).not.toHaveBeenCalled();
	});
});
