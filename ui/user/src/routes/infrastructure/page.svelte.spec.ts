import { page as appPage } from '$app/state';
import { Group } from '$lib/services';
import type { Infrastructure } from '$lib/services/admin/infrastructure';
import { createMockProfile, preparePageData } from '../../tests/helpers/pageData';
import { worker } from '../../tests/mocks/worker';
import InfrastructurePage from './+page.svelte';
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

async function renderPage(view: string) {
	(appPage.url as URL).searchParams.set('view', view);
	await preparePageData({ profile: createMockProfile([Group.ADMIN]) });
	worker.use(http.get('*/api/infrastructure', () => HttpResponse.json(infra)));
	return render(InfrastructurePage);
}

describe('Infrastructure page', () => {
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
