import { Group } from '$lib/services';
import type { Infrastructure } from '$lib/services/admin/infrastructure';
import { createMockProfile, preparePageData } from '../../tests/helpers/pageData';
import { getTokenUsageStatsResponse } from '../../tests/mocks/data';
import { worker } from '../../tests/mocks/worker';
import DashboardPage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

const now = new Date().toISOString();

const infra: Infrastructure = {
	engine: 'kubernetes',
	kubernetes: true,
	generatedAt: now,
	problems: [
		{ severity: 'error', area: 'mcp', name: 'GitHub', message: 'Pod is CrashLoopBackOff' }
	],
	obot: {
		enabled: true,
		workloads: [
			{
				name: 'obot',
				namespace: 'obot',
				kind: 'obot',
				health: 'healthy',
				replicas: 1,
				readyReplicas: 1,
				restarts: 0,
				created: now,
				warningEvents: 0,
				pods: [],
				events: []
			}
		]
	},
	mcpServers: {
		enabled: true,
		workloads: [
			{
				name: 'ms1crash',
				namespace: 'obot-mcp',
				kind: 'mcpServer',
				health: 'failing',
				replicas: 1,
				readyReplicas: 0,
				restarts: 5,
				created: now,
				warningEvents: 1,
				pods: [],
				events: []
			}
		]
	},
	claude: { enabled: false, workloads: [] }
};

function mockHubApis(overrides: Parameters<typeof worker.use> = []) {
	worker.use(
		...overrides,
		http.get('*/api/token-usage-stats', () =>
			HttpResponse.json({
				...getTokenUsageStatsResponse,
				totals: {
					requests: 1200,
					successes: 1188,
					fallbacks: 0,
					avgDurationMs: 900,
					inputTokens: 1,
					outputTokens: 1,
					totalTokens: 2,
					totalSpend: 42.5
				},
				topModelsBySpend: [
					{
						model: 'claude-sonnet',
						provider: 'anthropic',
						requests: 800,
						successes: 800,
						fallbacks: 0,
						avgDurationMs: 900,
						inputTokens: 1,
						outputTokens: 1,
						totalTokens: 2,
						totalSpend: 30
					}
				]
			})
		),
		http.get('*/api/token-usage', () => HttpResponse.json({ items: [] })),
		http.get('*/api/mcp-stats', () =>
			HttpResponse.json({
				items: [],
				timeStart: now,
				timeEnd: now,
				totalCalls: 321,
				uniqueUsers: 4
			})
		),
		http.get('*/api/mcp-server-requests', () =>
			HttpResponse.json({
				items: [
					{ id: 'r1', state: 'pending' },
					{ id: 'r2', state: 'approved' }
				]
			})
		),
		http.get('*/api/skills', () => HttpResponse.json({ items: [{ id: 's1', type: 'skill' }] })),
		http.get('*/api/infrastructure', () => HttpResponse.json(infra)),
		http.get('*/api/devices/scan-stats', () =>
			HttpResponse.json({
				timeStart: now,
				timeEnd: now,
				deviceCount: 17,
				userCount: 9,
				clients: [{ name: 'Claude Code', deviceCount: 12 }],
				mcpServers: [],
				skills: [],
				scanTimestamps: []
			})
		),
		http.get('*/api/provenance/supervisor/status', () =>
			HttpResponse.json({ enabled: true, pendingApprovals: 3 })
		),
		http.get('*/api/provenance/sessions', ({ request }) => {
			const risky = new URL(request.url).searchParams.has('min_risk');
			return HttpResponse.json({ items: [], total: risky ? 2 : 25 });
		})
	);
}

async function renderDashboard(groups: string[]) {
	const data = await preparePageData({ profile: createMockProfile(groups) });
	return render(DashboardPage, { data } as never);
}

const card = (id: string) => page.getByTestId(`hub-card-${id}`);

describe('Dashboard hub', () => {
	it('leads with the last 7 days and a briefing from Obot AI', async () => {
		mockHubApis();
		await renderDashboard([Group.ADMIN]);

		await expect
			.element(page.getByRole('link', { name: 'Ask Obot AI for a briefing' }))
			.toHaveAttribute('href', expect.stringMatching(/^\/ai\?ask=/));
		await expect
			.element(page.getByRole('link', { name: 'LLM spend' }).getByText('$42.50'))
			.toBeVisible();
		await expect
			.element(page.getByRole('link', { name: 'MCP tool calls' }).getByText('321'))
			.toBeVisible();
		await expect
			.element(page.getByRole('link', { name: 'Agent sessions' }).getByText('2 high-risk'))
			.toBeVisible();
	});

	it('flags what needs attention in each area', async () => {
		mockHubApis();
		await renderDashboard([Group.ADMIN]);

		await expect
			.element(card('supervisor').getByRole('link', { name: '3 approvals waiting for a decision' }))
			.toHaveAttribute('href', '/approvals');
		await expect
			.element(card('connect').getByRole('link', { name: '1 server requests waiting for review' }))
			.toHaveAttribute('href', '/mcp-server-requests');
		await expect
			.element(
				card('infrastructure').getByRole('link', { name: 'GitHub: Pod is CrashLoopBackOff' })
			)
			.toHaveAttribute('href', '/infrastructure');
	});

	it('shows every area to admins, with links into each', async () => {
		mockHubApis();
		await renderDashboard([Group.ADMIN]);

		await expect.element(card('llm').getByText('Top model: claude-sonnet · $30.00')).toBeVisible();
		await expect
			.element(card('devices').getByText('Most common client: Claude Code on 12 devices'))
			.toBeVisible();
		for (const id of ['mcp', 'skills', 'identity', 'platform', 'ai']) {
			await expect.element(card(id)).toBeVisible();
		}
		await expect
			.element(card('mcp').getByRole('link', { name: 'vMCPs' }))
			.toHaveAttribute('href', '/vmcps');
	});

	it('hides admin-only areas from power users', async () => {
		mockHubApis();
		await renderDashboard([Group.POWERUSER]);

		await expect.element(card('connect')).toBeVisible();
		await expect.element(card('mcp')).toBeVisible();
		await expect.element(card('ai')).toBeVisible();
		for (const id of ['supervisor', 'llm', 'infrastructure', 'devices', 'platform']) {
			await expect.element(card(id)).not.toBeInTheDocument();
		}
		await expect.element(page.getByRole('link', { name: 'LLM spend' })).not.toBeInTheDocument();
		await expect.element(page.getByRole('link', { name: 'MCP tool calls' })).toBeVisible();
	});

	it('keeps a failing card with its links and hides one the viewer may not use', async () => {
		mockHubApis([
			http.get('*/api/infrastructure', () => new HttpResponse('boom', { status: 500 })),
			http.get('*/api/skills', () => new HttpResponse('forbidden', { status: 403 }))
		]);
		await renderDashboard([Group.ADMIN]);

		await expect
			.element(card('infrastructure').getByText(/Numbers aren't available right now/))
			.toBeVisible();
		await expect
			.element(card('infrastructure').getByRole('link', { name: 'Logs' }))
			.toHaveAttribute('href', '/infrastructure?view=logs');
		await expect.element(card('identity')).toBeVisible();
		await expect.element(card('skills')).not.toBeInTheDocument();
	});
});
