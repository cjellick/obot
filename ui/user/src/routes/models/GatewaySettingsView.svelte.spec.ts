import { Group } from '$lib/services';
import { createMockProfile, preparePageData } from '../../tests/helpers/pageData';
import { worker } from '../../tests/mocks/worker';
import SettingsPage from './GatewaySettingsView.svelte';
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

const clientConfig = {
	drop_excess_requests: false,
	enable_logging: true,
	disable_content_logging: false,
	log_retention_days: 30,
	max_request_body_size_mb: 100,
	prometheus_labels: null,
	// Fields the page does not edit must be sent back unchanged.
	initial_pool_size: 300
};

const frameworkConfig = { pricing_sync_interval: 86400 };

function mockGateway({ status = readyStatus }: { status?: typeof readyStatus } = {}) {
	worker.use(
		http.get('/api/llm-gateway/status', () => HttpResponse.json(status)),
		http.get('/api/llm-gateway/admin/config', () =>
			HttpResponse.json({
				client_config: clientConfig,
				framework_config: frameworkConfig,
				is_logs_connected: true
			})
		),
		http.put('/api/llm-gateway/admin/config', () => HttpResponse.json({})),
		http.get('/api/llm-gateway/settings', () =>
			HttpResponse.json({ subscriptionPassthrough: false })
		),
		http.put('/api/llm-gateway/settings', () => HttpResponse.json({}))
	);
}

async function renderSettingsPage(groups: string[] = [Group.ADMIN]) {
	await preparePageData({ profile: createMockProfile(groups) });
	return render(SettingsPage);
}

function save() {
	return page.getByRole('button', { name: 'Save', exact: true });
}

describe('Models gateway settings tab', () => {
	it('loads the current logging and traffic settings', async () => {
		mockGateway();
		await renderSettingsPage();

		await expect.element(page.getByRole('switch', { name: 'Log requests' })).toBeChecked();
		await expect
			.element(page.getByRole('switch', { name: 'Store request and response content' }))
			.toBeChecked();
		await expect.element(page.getByLabelText('Log retention (days)')).toHaveValue(30);
		await expect.element(page.getByLabelText('Max request body (MB)')).toHaveValue(100);
		await expect
			.element(
				page.getByRole('switch', { name: "Reject requests when a provider's queue is full" })
			)
			.not.toBeChecked();
	});

	it('loads and saves whether Claude subscriptions are allowed', async () => {
		mockGateway();
		const saved = vi.fn();
		worker.use(
			http.get('/api/llm-gateway/settings', () =>
				HttpResponse.json({ subscriptionPassthrough: false })
			),
			http.put('/api/llm-gateway/settings', async ({ request }) => {
				saved(await request.json());
				return HttpResponse.json({});
			})
		);
		await renderSettingsPage();

		const toggle = page.getByRole('switch', { name: 'Allow Claude subscriptions' });
		await expect.element(toggle).not.toBeChecked();
		await toggle.click();
		await save().click();

		await vi.waitFor(() => expect(saved).toHaveBeenCalledOnce());
		expect(saved).toHaveBeenCalledWith({ subscriptionPassthrough: true });
	});

	it('sends the full client and framework config when saving', async () => {
		mockGateway();
		const saved = vi.fn();
		worker.use(
			http.put('/api/llm-gateway/admin/config', async ({ request }) => {
				saved(await request.json());
				return HttpResponse.json({});
			})
		);
		await renderSettingsPage();

		await page.getByRole('switch', { name: 'Store request and response content' }).click();
		await page
			.getByRole('switch', { name: "Reject requests when a provider's queue is full" })
			.click();
		await page.getByLabelText('Log retention (days)').fill('7');
		await save().click();

		await vi.waitFor(() => expect(saved).toHaveBeenCalledOnce());
		// The settings write follows; let it finish so it can't reach the next test's mocks.
		await expect.element(save()).toBeEnabled();
		expect(saved).toHaveBeenCalledWith({
			client_config: {
				...clientConfig,
				enable_logging: true,
				disable_content_logging: true,
				drop_excess_requests: true,
				log_retention_days: 7
			},
			framework_config: frameworkConfig
		});
	});

	it('turns request logging off only when the toggle is switched off', async () => {
		mockGateway();
		const saved = vi.fn();
		worker.use(
			http.put('/api/llm-gateway/admin/config', async ({ request }) => {
				saved(await request.json());
				return HttpResponse.json({});
			})
		);
		await renderSettingsPage();

		await page.getByRole('switch', { name: 'Log requests' }).click();
		await save().click();

		await vi.waitFor(() => expect(saved).toHaveBeenCalledOnce());
		// The settings write follows; let it finish so it can't reach the next test's mocks.
		await expect.element(save()).toBeEnabled();
		expect(saved).toHaveBeenCalledWith({
			client_config: { ...clientConfig, enable_logging: false },
			framework_config: frameworkConfig
		});
	});

	it('shows settings read-only to auditors', async () => {
		mockGateway();
		await renderSettingsPage([Group.USER, Group.AUDITOR]);

		await expect.element(page.getByRole('switch', { name: 'Log requests' })).toBeDisabled();
		await expect.element(page.getByLabelText('Log retention (days)')).toBeDisabled();
		await expect
			.element(page.getByRole('switch', { name: 'Allow Claude subscriptions' }))
			.toBeDisabled();
		await expect
			.element(page.getByRole('button', { name: 'Save', exact: true }))
			.not.toBeInTheDocument();
	});

	it('shows only the disabled banner when the gateway is disabled', async () => {
		mockGateway({ status: { ...readyStatus, enabled: false, ready: false } });
		await renderSettingsPage();

		await expect
			.element(page.getByText('The LLM gateway is disabled on this Obot server', { exact: false }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('switch', { name: 'Log requests' }))
			.not.toBeInTheDocument();
	});
});
