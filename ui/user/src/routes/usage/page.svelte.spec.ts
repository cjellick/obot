import { page as appPage } from '$app/state';
import { Group } from '$lib/services';
import { createMockProfile, preparePageData } from '../../tests/helpers/pageData';
import { worker } from '../../tests/mocks/worker';
import UsagePage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

const startingStatus = {
	enabled: true,
	ready: false,
	hasProviders: true,
	modelProviderName: 'llm-gateway-model-provider',
	proxyPath: '/api/llm-proxy'
};

async function renderUsagePage(groups: string[], view: string) {
	appPage.url.searchParams.set('view', view);
	await preparePageData({ profile: createMockProfile(groups) });
	return render(UsagePage);
}

describe('Usage page gateway tab', () => {
	it('shows gateway analytics to admins', async () => {
		worker.use(http.get('/api/llm-gateway/status', () => HttpResponse.json(startingStatus)));
		await renderUsagePage([Group.ADMIN], 'gateway');

		await expect.element(page.getByRole('button', { name: 'Gateway', exact: true })).toBeVisible();
		await expect.element(page.getByText('The LLM gateway is starting…')).toBeInTheDocument();
	});

	it('hides the gateway tab from users without admin access', async () => {
		const mcpStats = vi.fn();
		worker.use(
			http.get('/api/mcp-stats', () => {
				mcpStats();
				return HttpResponse.json({
					items: [],
					timeStart: '',
					timeEnd: '',
					totalCalls: 0,
					uniqueUsers: 0
				});
			})
		);
		await renderUsagePage([Group.USER], 'gateway');

		await expect
			.element(page.getByRole('button', { name: 'Gateway', exact: true }))
			.not.toBeInTheDocument();
		await expect.element(page.getByText('The LLM gateway is starting…')).not.toBeInTheDocument();
		// The MCP tab is shown instead; let its stats request settle inside this test.
		await vi.waitFor(() => expect(mcpStats).toHaveBeenCalled());
	});
});
