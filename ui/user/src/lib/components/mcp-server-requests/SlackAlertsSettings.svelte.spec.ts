import { createMockProfile, preparePageData } from '../../../tests/helpers/pageData';
import { worker } from '../../../tests/mocks/worker';
import SlackAlertsSettings from './SlackAlertsSettings.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

async function renderSettings() {
	await preparePageData({ profile: createMockProfile() });
	return render(SlackAlertsSettings);
}

describe('SlackAlertsSettings', () => {
	it('saves a webhook and shows only a hint of it', async () => {
		const saved = vi.fn();
		worker.use(
			http.put('/api/mcp-server-request-settings', async ({ request }) => {
				saved(await request.json());
				return HttpResponse.json({
					slackWebhookConfigured: true,
					slackWebhookHint: 'https://hooks.slack.com/…1234',
					setViaDeployment: false
				});
			})
		);
		await renderSettings();

		await expect.element(page.getByText('Not set up.', { exact: false })).toBeInTheDocument();
		await page
			.getByLabelText('Incoming webhook URL')
			.fill('https://hooks.slack.com/services/T1/B2/abcd1234');
		await page.getByRole('button', { name: 'Save' }).click();

		await vi.waitFor(() =>
			expect(saved).toHaveBeenCalledWith({
				slackWebhookURL: 'https://hooks.slack.com/services/T1/B2/abcd1234'
			})
		);
		await expect
			.element(page.getByText('Posting to https://hooks.slack.com/…1234'))
			.toBeInTheDocument();
		await expect.element(page.getByLabelText('Incoming webhook URL')).not.toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Remove' })).toBeInTheDocument();
	});

	it('sends a test message', async () => {
		const tested = vi.fn();
		worker.use(
			http.get('/api/mcp-server-request-settings', () =>
				HttpResponse.json({
					slackWebhookConfigured: true,
					slackWebhookHint: 'https://hooks.slack.com/…1234',
					setViaDeployment: false
				})
			),
			http.post('/api/mcp-server-request-settings/test', () => {
				tested();
				return new HttpResponse(null, { status: 204 });
			})
		);
		await renderSettings();

		await page.getByRole('button', { name: 'Send a test message' }).click();
		await vi.waitFor(() => expect(tested).toHaveBeenCalled());
	});

	it('is read-only when the webhook was set at deployment', async () => {
		worker.use(
			http.get('/api/mcp-server-request-settings', () =>
				HttpResponse.json({
					slackWebhookConfigured: true,
					slackWebhookHint: 'https://hooks.slack.com/…cret',
					setViaDeployment: true
				})
			)
		);
		await renderSettings();

		await expect
			.element(page.getByText('Set when Obot was deployed', { exact: false }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('button', { name: 'Send a test message' }))
			.toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Replace' })).not.toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Remove' })).not.toBeInTheDocument();
		await expect.element(page.getByLabelText('Incoming webhook URL')).not.toBeInTheDocument();
	});
});
