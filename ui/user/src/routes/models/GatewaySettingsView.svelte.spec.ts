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

const semanticCachePlugin = {
	name: 'semantic_cache',
	enabled: true,
	config: {
		provider: 'openai',
		embedding_model: 'text-embedding-3-large',
		dimension: 3072,
		ttl: '10m',
		threshold: 0.9,
		cache_by_model: true,
		cache_by_provider: false,
		exclude_system_prompt: true
	},
	status: { status: 'active' }
};

function mockGateway({
	status = readyStatus,
	cacheConnected = true,
	plugins = [] as unknown[]
}: { status?: typeof readyStatus; cacheConnected?: boolean; plugins?: unknown[] } = {}) {
	worker.use(
		http.get('/api/llm-gateway/status', () => HttpResponse.json(status)),
		http.get('/api/llm-gateway/admin/config', () =>
			HttpResponse.json({
				client_config: clientConfig,
				framework_config: frameworkConfig,
				is_cache_connected: cacheConnected,
				is_logs_connected: true
			})
		),
		http.get('/api/llm-gateway/admin/plugins', () => HttpResponse.json({ plugins })),
		// The single Save also writes the cache plugin; specs that care override these.
		http.post('/api/llm-gateway/admin/plugins', () => HttpResponse.json({})),
		http.put('/api/llm-gateway/admin/plugins/:name', () => HttpResponse.json({})),
		http.put('/api/llm-gateway/admin/config', () => HttpResponse.json({})),
		http.get('/api/llm-gateway/settings', () =>
			HttpResponse.json({ subscriptionPassthrough: false })
		),
		http.put('/api/llm-gateway/settings', () => HttpResponse.json({})),
		http.get('/api/llm-gateway/admin/providers', () =>
			HttpResponse.json({
				providers: [
					{
						name: 'openai',
						network_config: {},
						concurrency_and_buffer_size: { concurrency: 1000, buffer_size: 5000 }
					}
				],
				total: 1
			})
		)
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
		// The cache plugin write follows; let it finish so it can't reach the next test's mocks.
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
		// The cache plugin write follows; let it finish so it can't reach the next test's mocks.
		await expect.element(save()).toBeEnabled();
		expect(saved).toHaveBeenCalledWith({
			client_config: { ...clientConfig, enable_logging: false },
			framework_config: frameworkConfig
		});
	});

	it('creates the cache plugin with exact-match settings and no shared cache key', async () => {
		mockGateway();
		const created = vi.fn();
		const updated = vi.fn();
		worker.use(
			http.post('/api/llm-gateway/admin/plugins', async ({ request }) => {
				created(await request.json());
				return HttpResponse.json({});
			}),
			http.put('/api/llm-gateway/admin/plugins/:name', () => {
				updated();
				return HttpResponse.json({});
			})
		);
		await renderSettingsPage();

		const enableCaching = page.getByRole('switch', { name: 'Enable caching' });
		await enableCaching.click();
		await expect.element(enableCaching).toBeChecked();
		await save().click();

		await vi.waitFor(() => expect(created).toHaveBeenCalledOnce());
		expect(updated).not.toHaveBeenCalled();
		expect(created).toHaveBeenCalledWith({
			name: 'semantic_cache',
			enabled: true,
			config: {
				ttl: '5m',
				cache_by_model: true,
				cache_by_provider: true,
				exclude_system_prompt: false,
				conversation_history_threshold: 3,
				vector_store_namespace: 'ObotLLMGatewayCache',
				default_cache_key: '',
				dimension: 1
			}
		});
	});

	it('updates an existing semantic cache plugin with its embedding settings', async () => {
		mockGateway({ plugins: [semanticCachePlugin] });
		const updated = vi.fn();
		worker.use(
			http.put('/api/llm-gateway/admin/plugins/:name', async ({ params, request }) => {
				updated(params.name, await request.json());
				return HttpResponse.json({});
			})
		);
		await renderSettingsPage();

		await expect.element(page.getByRole('switch', { name: 'Enable caching' })).toBeChecked();
		await expect.element(page.getByText('Plugin status: active')).toBeInTheDocument();
		await expect
			.element(page.getByLabelText('Embedding model'))
			.toHaveValue('text-embedding-3-large');
		await page.getByLabelText('Similarity threshold (0–1)').fill('0.95');
		await save().click();

		await vi.waitFor(() => expect(updated).toHaveBeenCalledOnce());
		expect(updated).toHaveBeenCalledWith('semantic_cache', {
			name: 'semantic_cache',
			enabled: true,
			config: {
				ttl: '10m',
				cache_by_model: true,
				cache_by_provider: false,
				exclude_system_prompt: true,
				conversation_history_threshold: 3,
				vector_store_namespace: 'ObotLLMGatewayCache',
				default_cache_key: '',
				provider: 'openai',
				embedding_model: 'text-embedding-3-large',
				dimension: 3072,
				threshold: 0.95
			}
		});
	});

	it('explains how to enable caching when no cache server is connected', async () => {
		mockGateway({ cacheConnected: false });
		await renderSettingsPage();

		await expect
			.element(page.getByText('Caching needs a Redis or Valkey server.', { exact: false }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('switch', { name: 'Enable caching' }))
			.not.toBeInTheDocument();
	});

	it('saves the general settings and the cache plugin with one Save', async () => {
		mockGateway();
		const saved = vi.fn();
		const created = vi.fn();
		worker.use(
			http.put('/api/llm-gateway/admin/config', async ({ request }) => {
				saved(await request.json());
				return HttpResponse.json({});
			}),
			http.post('/api/llm-gateway/admin/plugins', async ({ request }) => {
				created(await request.json());
				return HttpResponse.json({});
			})
		);
		await renderSettingsPage();

		await page.getByLabelText('Log retention (days)').fill('14');
		const enableCaching = page.getByRole('switch', { name: 'Enable caching' });
		await enableCaching.click();
		await expect.element(enableCaching).toBeChecked();
		await save().click();

		await vi.waitFor(() => expect(created).toHaveBeenCalledOnce());
		expect(saved).toHaveBeenCalledWith(
			expect.objectContaining({
				client_config: expect.objectContaining({ log_retention_days: 14 })
			})
		);
		expect(created).toHaveBeenCalledWith(expect.objectContaining({ enabled: true }));
	});

	it('does not write the cache plugin when no cache server is connected', async () => {
		mockGateway({ cacheConnected: false });
		const saved = vi.fn();
		const created = vi.fn();
		worker.use(
			http.put('/api/llm-gateway/admin/config', async ({ request }) => {
				saved(await request.json());
				return HttpResponse.json({});
			}),
			http.post('/api/llm-gateway/admin/plugins', () => {
				created();
				return HttpResponse.json({});
			})
		);
		await renderSettingsPage();

		await save().click();

		await vi.waitFor(() => expect(saved).toHaveBeenCalledOnce());
		// The cache plugin write follows; let it finish so it can't reach the next test's mocks.
		await expect.element(save()).toBeEnabled();
		expect(created).not.toHaveBeenCalled();
	});

	it('shows settings read-only to auditors', async () => {
		mockGateway();
		await renderSettingsPage([Group.USER, Group.AUDITOR]);

		await expect.element(page.getByRole('switch', { name: 'Log requests' })).toBeDisabled();
		await expect.element(page.getByLabelText('Log retention (days)')).toBeDisabled();
		await expect.element(page.getByRole('switch', { name: 'Enable caching' })).toBeDisabled();
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
