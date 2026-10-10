import { page as appPage } from '$app/state';
import { Group } from '$lib/services';
import { createMockProfile, preparePageData } from '../../tests/helpers/pageData';
import { worker } from '../../tests/mocks/worker';
import AuditLogsPage from './+page.svelte';
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

function mockGateway() {
	const logDetails = vi.fn();
	worker.use(
		http.get('/api/llm-gateway/status', () => HttpResponse.json(readyStatus)),
		http.get('/api/llm-gateway/admin/logs', () =>
			HttpResponse.json({ logs: [successLog, errorLog], pagination: { total_count: 2 } })
		),
		http.get('/api/llm-gateway/admin/logs/:id', ({ params }) => {
			logDetails(params.id);
			return HttpResponse.json(
				params.id === 'log-2'
					? {
							...errorLog,
							error_details: { error: { message: 'upstream overloaded' } },
							routing_rule_name: 'Cheaper GPT'
						}
					: successLog
			);
		})
	);
	return { logDetails };
}

async function renderAuditLogsPage(groups: string[] = [Group.ADMIN], view = 'gateway') {
	appPage.url.searchParams.set('view', view);
	await preparePageData({ profile: createMockProfile(groups) });
	return render(AuditLogsPage);
}

describe('Audit logs page gateway requests', () => {
	it('lists gateway requests with their user, key, retries and fallbacks', async () => {
		mockGateway();
		await renderAuditLogsPage();

		await expect.element(page.getByRole('button', { name: 'Gateway Requests' })).toBeVisible();
		await expect.element(page.getByRole('cell', { name: 'alice' })).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: 'openai-key-1' })).toBeInTheDocument();
		await expect.element(page.getByText('1 retries')).toBeInTheDocument();
		await expect.element(page.getByText('fallback', { exact: true })).toBeInTheDocument();
		await expect.element(page.getByText('chat_completion · stream')).toBeInTheDocument();
		await expect
			.element(page.getByRole('button', { name: 'Create Export' }))
			.not.toBeInTheDocument();
	});

	it('shows request details with the error and routing rule', async () => {
		const { logDetails } = mockGateway();
		await renderAuditLogsPage();

		await page.getByRole('cell', { name: 'bob' }).click();

		const details = page.getByRole('dialog', { name: 'Request details' });
		await vi.waitFor(() => expect(logDetails).toHaveBeenCalledWith('log-2'));
		await expect.element(details.getByText('upstream overloaded')).toBeInTheDocument();
		await expect.element(details.getByText('Cheaper GPT')).toBeInTheDocument();
		await expect.element(details.getByText('#1')).toBeInTheDocument();
	});

	it('shows the gateway requests tab to auditors', async () => {
		mockGateway();
		await renderAuditLogsPage([Group.USER, Group.AUDITOR]);

		await expect.element(page.getByRole('button', { name: 'Gateway Requests' })).toBeVisible();
	});

	it('hides the gateway requests tab from users without admin access', async () => {
		await renderAuditLogsPage([Group.USER], 'mcp');

		await expect
			.element(page.getByRole('button', { name: 'Gateway Requests' }))
			.not.toBeInTheDocument();
	});
});
