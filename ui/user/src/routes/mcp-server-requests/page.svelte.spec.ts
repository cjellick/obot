import { Group } from '$lib/services';
import type { MCPServerRequest } from '$lib/services/mcpserverrequests';
import { createMockProfile, preparePageData } from '../../tests/helpers/pageData';
import { worker } from '../../tests/mocks/worker';
import RequestsPage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

const pending: MCPServerRequest = {
	id: 'msrq1pending',
	created: new Date().toISOString(),
	name: 'Sentry',
	reason: 'Debug production errors from my editor',
	runtime: 'remote',
	remoteURL: 'https://mcp.sentry.dev/mcp',
	requesterID: '3',
	requesterName: 'Dev User',
	state: 'pending'
};

function mockRequests(requested = vi.fn()) {
	worker.use(
		http.get('/api/mcp-server-requests', ({ request }) => {
			const state = new URL(request.url).searchParams.get('state');
			requested(state);
			return HttpResponse.json({ items: state === 'approved' ? [] : [pending] });
		})
	);
	return requested;
}

async function renderPage(groups: string[] = [Group.ADMIN]) {
	localStorage.setItem('seenSplashDialog', new Date().toISOString());
	await preparePageData({ profile: createMockProfile(groups) });
	return render(RequestsPage);
}

describe('Server requests', () => {
	it('lists pending requests with their requester for administrators', async () => {
		const requested = mockRequests();
		await renderPage();

		await vi.waitFor(() => expect(requested).toHaveBeenCalledWith('pending'));
		const link = page.getByRole('link', { name: /Sentry/ });
		await expect.element(link).toHaveAttribute('href', '/mcp-server-requests/msrq1pending');
		await expect.element(link.getByText('https://mcp.sentry.dev/mcp')).toBeInTheDocument();
		await expect.element(link.getByText(/Requested by Dev User/)).toBeInTheDocument();
	});

	it('switches state filters', async () => {
		const requested = mockRequests();
		await renderPage();

		await page.getByRole('button', { name: 'Approved' }).click();
		await vi.waitFor(() => expect(requested).toHaveBeenCalledWith('approved'));
		await expect.element(page.getByText('No requests here.')).toBeInTheDocument();
	});

	it('shows administrators both built-in servers and the managed settings', async () => {
		mockRequests();
		await renderPage();

		const setup = page.getByRole('region', { name: 'Your Obot MCP servers' });
		await expect.element(setup.getByText(/\/mcp-connect\/obot$/).first()).toBeInTheDocument();
		await expect.element(setup.getByText(/\/mcp-connect\/obot-admin$/)).toBeInTheDocument();
		await expect.element(setup.getByText('Administrators only.', { exact: false })).toBeVisible();

		// Deployment snippets sit collapsed in the Claude Code and Codex cards.
		const claude = setup.getByRole('article', { name: 'Claude Code' });
		await expect.element(claude.getByText('managed-mcp.json', { exact: true })).not.toBeVisible();
		await claude.getByText('Deploy to everyone').click();
		await expect.element(claude.getByText('managed-mcp.json', { exact: true })).toBeVisible();
		await expect.element(claude.getByText('Claude managed settings')).toBeVisible();

		const codex = setup.getByRole('article', { name: 'Codex' });
		await codex.getByText('Deploy to everyone').click();
		await expect.element(codex.getByText('Codex managed config.toml')).toBeVisible();
		await expect.element(codex.getByText(/^\[mcp_servers\.obot\]/)).toBeVisible();
		await expect.element(page.getByRole('region', { name: 'Slack alerts' })).toBeInTheDocument();
	});

	it('shows basic users only their own server and no requester names', async () => {
		mockRequests();
		await renderPage([Group.USER]);

		const setup = page.getByRole('region', { name: 'Your Obot MCP servers' });
		await expect.element(setup.getByText(/\/mcp-connect\/obot$/).first()).toBeInTheDocument();
		await expect.element(setup.getByText(/\/mcp-connect\/obot-admin$/)).not.toBeInTheDocument();
		await expect.element(setup.getByText('Deploy to everyone')).not.toBeInTheDocument();
		await expect.element(setup.getByText('managed-mcp.json')).not.toBeInTheDocument();
		await expect
			.element(setup.getByText(/^claude mcp add --transport http obot /))
			.toBeInTheDocument();
		await expect.element(setup.getByText(/^codex mcp add obot --url /)).toBeInTheDocument();
		await expect
			.element(page.getByRole('region', { name: 'Slack alerts' }))
			.not.toBeInTheDocument();
		const link = page.getByRole('link', { name: /Sentry/ });
		await expect.element(link).toBeInTheDocument();
		await expect.element(link.getByText(/Requested by/)).not.toBeInTheDocument();
	});

	it('gives every client a card with its own way to add the user server', async () => {
		mockRequests();
		await renderPage([Group.USER]);

		const setup = page.getByRole('region', { name: 'Your Obot MCP servers' });
		const vscode = setup.getByRole('article', { name: 'VS Code / GitHub Copilot' });
		await expect
			.element(
				vscode.getByText(
					/^code --add-mcp '\{"name":"obot","type":"http","url":"http.*\/mcp-connect\/obot"\}'$/
				)
			)
			.toBeInTheDocument();
		await expect.element(vscode.getByText(/"servers"/)).toBeInTheDocument();

		const cursor = setup.getByRole('article', { name: 'Cursor' });
		await expect.element(cursor.getByText(/"mcpServers"/)).toBeInTheDocument();
		const install = cursor.getByRole('link', { name: 'Add to Cursor' });
		const href = install.element().getAttribute('href') ?? '';
		expect(href).toMatch(/^cursor:\/\/anysphere\.cursor-deeplink\/mcp\/install\?name=obot&config=/);
		const config = decodeURIComponent(new URL(href).searchParams.get('config') ?? '');
		expect(JSON.parse(atob(config)).url).toMatch(/\/mcp-connect\/obot$/);
	});
});
