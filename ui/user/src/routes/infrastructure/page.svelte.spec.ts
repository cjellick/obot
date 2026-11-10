import { page as appPage } from '$app/state';
import { Group } from '$lib/services';
import type { InfraWorkload, Infrastructure } from '$lib/services/admin/infrastructure';
import { goto } from '$lib/url';
import { createMockProfile, preparePageData } from '../../tests/helpers/pageData';
import { worker } from '../../tests/mocks/worker';
import InfrastructurePage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

vi.mock('$lib/url', async (importOriginal) => ({
	...(await importOriginal<typeof import('$lib/url')>()),
	goto: vi.fn()
}));

const now = new Date().toISOString();

const infra: Infrastructure = {
	engine: 'kubernetes',
	kubernetes: true,
	generatedAt: now,
	problems: [
		{
			severity: 'error',
			area: 'mcp',
			name: 'GitHub',
			workload: 'ms1crash',
			message: 'Pod ms1crash-a is CrashLoopBackOff',
			link: '/mcp-servers/c/github'
		}
	],
	obot: { enabled: true, namespace: 'obot', workloads: [] },
	mcpServers: {
		enabled: true,
		namespace: 'obot-mcp',
		workloads: [
			{
				name: 'ms1crash',
				namespace: 'obot-mcp',
				displayName: 'GitHub',
				kind: 'mcpServer',
				health: 'failing',
				healthReason: 'Pod ms1crash-a is CrashLoopBackOff',
				replicas: 1,
				readyReplicas: 0,
				restarts: 5,
				image: 'ghcr.io/example/github:1',
				cpuRequest: '100m',
				memoryLimit: '512Mi',
				created: now,
				catalogEntryID: 'github',
				catalogEntryName: 'GitHub',
				link: '/mcp-servers/c/github',
				warningEvents: 1,
				pods: [
					{
						name: 'ms1crash-a',
						phase: 'Running',
						status: 'CrashLoopBackOff',
						ready: '0/1',
						restarts: 5,
						lastTermination: 'OOMKilled (exit 137)',
						created: now
					}
				],
				events: [
					{
						type: 'Warning',
						reason: 'BackOff',
						message: 'Back-off restarting failed container',
						object: 'pod/ms1crash-a',
						lastSeen: now
					}
				]
			}
		]
	},
	claude: { enabled: false, workloads: [] }
};

async function renderPage(view: string, data: Infrastructure = infra) {
	(appPage.url as URL).searchParams.set('view', view);
	await preparePageData({ profile: createMockProfile([Group.ADMIN]) });
	worker.use(http.get('*/api/infrastructure', () => HttpResponse.json(data)));
	return render(InfrastructurePage);
}

const healthyServer = {
	...infra.mcpServers.workloads[0],
	name: 'ms1docs',
	displayName: 'Docs',
	health: 'healthy',
	healthReason: undefined,
	readyReplicas: 1,
	restarts: 0,
	image: 'ghcr.io/example/docs:2',
	catalogEntryName: undefined,
	vmcpID: 'vmcp1',
	vmcpName: 'Engineering',
	warningEvents: 0,
	pods: [],
	events: []
} satisfies InfraWorkload;

const withClaude: Infrastructure = {
	...infra,
	mcpServers: {
		...infra.mcpServers,
		workloads: [healthyServer, ...infra.mcpServers.workloads]
	},
	claude: {
		enabled: true,
		namespace: 'obot-claude',
		workloads: [
			{
				name: 'pool-eng',
				namespace: 'obot-claude',
				displayName: 'Engineering sandbox',
				environmentName: 'Engineering sandbox',
				kind: 'claudeWorkerPool',
				health: 'healthy',
				replicas: 2,
				readyReplicas: 2,
				restarts: 0,
				created: now,
				warningEvents: 0,
				events: [],
				pods: [
					{
						name: 'pool-eng-a',
						phase: 'Running',
						status: 'Running',
						ready: '1/1',
						restarts: 0,
						created: now,
						sessions: [{ id: 's1', name: 'Fix login bug', userName: 'alice', created: now }]
					},
					{
						name: 'pool-eng-b',
						phase: 'Running',
						status: 'Running',
						ready: '1/1',
						restarts: 0,
						created: now
					}
				]
			}
		]
	}
};

describe('Infrastructure page', () => {
	it('summarizes MCP servers on the overview, failing first', async () => {
		await renderPage('overview', withClaude);
		const section = page.getByRole('region', { name: 'MCP servers' });
		await expect.element(section.getByRole('link', { name: 'GitHub' })).toBeVisible();
		await expect.element(section.getByRole('link', { name: 'Docs' })).toBeVisible();
		await expect.element(section.getByText('vMCP: Engineering')).toBeVisible();
		await expect.element(section.getByText('docs:2')).toBeVisible();
		await expect
			.element(section.getByRole('link', { name: 'View all 2' }))
			.toHaveAttribute('href', '/infrastructure?view=mcp');
		await expect.element(section.getByRole('img', { name: 'Health breakdown' })).toBeVisible();
		const rows = section.getByRole('row');
		await expect.element(rows.nth(2)).toBeInTheDocument();
		expect(rows.nth(1).element().textContent).toContain('GitHub');
		expect(rows.nth(2).element().textContent).toContain('Docs');
	});

	it('summarizes Claude environments with their workers and sessions', async () => {
		await renderPage('overview', withClaude);
		const section = page.getByRole('region', { name: 'Claude Environments' });
		await expect.element(section.getByText('Engineering sandbox')).toBeVisible();
		await expect.element(section.getByText('2/2 workers')).toBeVisible();
		await expect.element(section.getByText('pool-eng-a')).toBeVisible();
		await expect.element(section.getByText('alice')).toBeVisible();
		await expect.element(section.getByText('Fix login bug')).toBeVisible();
		await expect.element(section.getByText('Idle')).toBeVisible();
		await expect
			.element(section.getByRole('link', { name: 'View all' }))
			.toHaveAttribute('href', '/infrastructure?view=claude');
	});

	it('shows empty states when nothing runs on Kubernetes yet', async () => {
		await renderPage('overview', {
			...infra,
			problems: [],
			mcpServers: { ...infra.mcpServers, workloads: [] },
			claude: { enabled: true, workloads: [] }
		});
		await expect
			.element(page.getByText('No MCP servers are running on Kubernetes yet.'))
			.toBeVisible();
		await expect
			.element(page.getByText('No Claude environments are running on Kubernetes yet.'))
			.toBeVisible();
	});

	it('opens a server on the MCP Servers tab from the overview', async () => {
		await renderPage('overview', withClaude);
		await page
			.getByRole('region', { name: 'MCP servers' })
			.getByRole('link', { name: 'GitHub' })
			.click();
		expect(goto).toHaveBeenCalledWith('/infrastructure?view=mcp');
	});

	it('lists problems first on the overview', async () => {
		await renderPage('overview');
		await expect.element(page.getByText('Pod ms1crash-a is CrashLoopBackOff')).toBeVisible();
		await expect
			.element(page.getByRole('link', { name: 'Open' }))
			.toHaveAttribute('href', '/mcp-servers/c/github');
	});

	it('shows an MCP server workload and its pods and events', async () => {
		await renderPage('mcp');
		await expect.element(page.getByText('ms1crash', { exact: true })).toBeVisible();
		await page.getByText('ms1crash', { exact: true }).click();
		await expect.element(page.getByText('OOMKilled (exit 137)')).toBeVisible();
		await expect.element(page.getByText('Back-off restarting failed container')).toBeVisible();
		await expect.element(page.getByRole('button', { name: 'Load logs' })).toBeVisible();
	});

	it('says when MCP servers do not run on Kubernetes', async () => {
		(appPage.url as URL).searchParams.set('view', 'mcp');
		await preparePageData({ profile: createMockProfile([Group.ADMIN]) });
		worker.use(
			http.get('*/api/infrastructure', () =>
				HttpResponse.json({
					...infra,
					engine: 'docker',
					kubernetes: false,
					message: 'Workload details need Kubernetes.',
					problems: []
				})
			)
		);
		render(InfrastructurePage);
		await expect.element(page.getByText('Workload details need Kubernetes.')).toBeVisible();
	});
});
