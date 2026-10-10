import { page as appPage } from '$app/state';
import { Group } from '$lib/services';
import { goto } from '$lib/url';
import { createMockProfile, preparePageData } from '../../../../tests/helpers/pageData';
import { worker } from '../../../../tests/mocks/worker';
import ProviderPage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

vi.mock(import('$lib/url'), async (importOriginal) => ({
	...(await importOriginal()),
	goto: vi.fn()
}));

const openai = {
	name: 'openai',
	network_config: { max_retries: 2, default_request_timeout_in_seconds: 300 },
	concurrency_and_buffer_size: { concurrency: 1000, buffer_size: 5000 },
	provider_status: 'active'
};

const existingKey = {
	id: 'k1',
	name: 'openai-key-1',
	value: { value: 'sk-***' },
	models: ['*'],
	weight: 1,
	enabled: true,
	status: 'success'
};

function mockProvider({
	provider = openai as Record<string, unknown>,
	keys = [existingKey] as unknown[]
} = {}) {
	const synced = vi.fn();
	worker.use(
		http.get('/api/llm-gateway/admin/providers/:name', () => HttpResponse.json(provider)),
		http.get('/api/llm-gateway/admin/providers', () =>
			HttpResponse.json({ providers: [provider], total: 1 })
		),
		http.get('/api/llm-gateway/admin/providers/:name/keys', () =>
			HttpResponse.json({ keys, total: keys.length })
		),
		http.get('/api/llm-gateway/admin/models', () =>
			HttpResponse.json({ models: [{ name: 'gpt-4o', provider: 'openai' }] })
		),
		http.post('/api/llm-gateway/sync-models', () => {
			synced();
			return HttpResponse.json({});
		})
	);
	return { synced };
}

async function renderProviderPage(groups: string[] = [Group.ADMIN], name = 'openai') {
	(appPage.params as Record<string, string>).name = name;
	await preparePageData({ profile: createMockProfile(groups) });
	return render(ProviderPage);
}

afterEach(() => {
	delete (appPage.params as Record<string, string>).name;
});

describe('LLM gateway provider page', () => {
	it('shows the provider keys and models', async () => {
		mockProvider();
		await renderProviderPage();

		await expect.element(page.getByText('openai-key-1')).toBeInTheDocument();
		await expect.element(page.getByText('sk-***')).toBeInTheDocument();
		await expect.element(page.getByText('healthy')).toBeInTheDocument();
		await expect.element(page.getByRole('heading', { name: 'Models (1)' })).toBeInTheDocument();
		await expect.element(page.getByLabelText('Max retries')).toHaveValue(2);
	});

	it('adds a key with the next free name and re-syncs models', async () => {
		const { synced } = mockProvider();
		const created = vi.fn();
		worker.use(
			http.post('/api/llm-gateway/admin/providers/:name/keys', async ({ params, request }) => {
				created(params.name, await request.json());
				return HttpResponse.json({ id: 'k2', name: 'openai-key-2' });
			})
		);
		await renderProviderPage();

		await page.getByRole('button', { name: 'Add Key' }).click();
		const dialog = page.getByRole('dialog');
		await expect.element(dialog.getByLabelText('Name')).toHaveValue('openai-key-2');
		await dialog.getByPlaceholder('sk-...').fill('sk-new');
		await dialog.getByLabelText('Allowed models').fill('gpt-4o, gpt-4.1');
		await dialog.getByRole('button', { name: 'Save', exact: true }).click();

		await vi.waitFor(() => expect(created).toHaveBeenCalledOnce());
		expect(created).toHaveBeenCalledWith('openai', {
			name: 'openai-key-2',
			models: ['gpt-4o', 'gpt-4.1'],
			blacklisted_models: [],
			weight: 1,
			enabled: true,
			value: 'sk-new'
		});
		await vi.waitFor(() => expect(synced).toHaveBeenCalled());
	});

	it('edits an existing key', async () => {
		mockProvider();
		const updated = vi.fn();
		worker.use(
			http.put('/api/llm-gateway/admin/providers/:name/keys/:id', async ({ params, request }) => {
				updated(params.name, params.id, await request.json());
				return HttpResponse.json(existingKey);
			})
		);
		await renderProviderPage();

		await page.getByRole('button', { name: 'Edit key' }).click();
		const dialog = page.getByRole('dialog');
		await expect.element(dialog.getByLabelText('Name')).toHaveValue('openai-key-1');
		await dialog.getByLabelText('Blocked models').fill('o1-pro');
		await dialog.getByLabelText('Load-balancing weight').fill('2');
		await dialog.getByRole('button', { name: 'Save', exact: true }).click();

		await vi.waitFor(() => expect(updated).toHaveBeenCalledOnce());
		expect(updated).toHaveBeenCalledWith('openai', 'k1', {
			name: 'openai-key-1',
			models: ['*'],
			blacklisted_models: ['o1-pro'],
			weight: 2,
			enabled: true,
			// The redacted value the API returned is sent back unchanged.
			value: 'sk-***'
		});
	});

	it('disables a key from its row toggle', async () => {
		mockProvider();
		const updated = vi.fn();
		worker.use(
			http.put('/api/llm-gateway/admin/providers/:name/keys/:id', async ({ params, request }) => {
				updated(params.id, await request.json());
				return HttpResponse.json(existingKey);
			})
		);
		await renderProviderPage();

		await page.getByRole('switch', { name: 'Enabled' }).click();

		await vi.waitFor(() => expect(updated).toHaveBeenCalledOnce());
		expect(updated).toHaveBeenCalledWith(
			'k1',
			expect.objectContaining({ name: 'openai-key-1', enabled: false, models: ['*'], weight: 1 })
		);
	});

	it('deletes a key after confirmation', async () => {
		mockProvider();
		const deleted = vi.fn();
		worker.use(
			http.delete('/api/llm-gateway/admin/providers/:name/keys/:id', ({ params }) => {
				deleted(params.name, params.id);
				return new HttpResponse(null, { status: 204 });
			})
		);
		await renderProviderPage();

		await page.getByRole('button', { name: 'Delete key' }).click();
		await expect.element(page.getByText('Delete key openai-key-1?')).toBeInTheDocument();
		await page.getByRole('button', { name: "Yes, I'm sure" }).click();

		await vi.waitFor(() => expect(deleted).toHaveBeenCalledWith('openai', 'k1'));
		expect(deleted).toHaveBeenCalledOnce();
	});

	it('deletes the provider and returns to the provider list', async () => {
		mockProvider();
		const deleted = vi.fn();
		worker.use(
			http.delete('/api/llm-gateway/admin/providers/:name', ({ params }) => {
				deleted(params.name);
				return new HttpResponse(null, { status: 204 });
			})
		);
		await renderProviderPage();

		await page.getByRole('button', { name: 'Delete provider' }).click();
		await page.getByRole('button', { name: "Yes, I'm sure" }).click();

		await vi.waitFor(() => expect(deleted).toHaveBeenCalledWith('openai'));
		await vi.waitFor(() => expect(goto).toHaveBeenCalledWith('/llm-gateway/providers'));
	});

	it('does not offer keys for a keyless custom provider', async () => {
		mockProvider({
			provider: {
				...openai,
				name: 'local-llm',
				custom_provider_config: { base_provider_type: 'openai', is_key_less: true }
			},
			keys: []
		});
		await renderProviderPage([Group.ADMIN], 'local-llm');

		await expect.element(page.getByText('This provider does not use keys.')).toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Add Key' })).not.toBeInTheDocument();
	});

	it('hides key management from auditors', async () => {
		mockProvider();
		await renderProviderPage([Group.USER, Group.AUDITOR]);

		await expect.element(page.getByText('openai-key-1')).toBeInTheDocument();
		await expect.element(page.getByRole('switch', { name: 'Enabled' })).toBeDisabled();
		await expect.element(page.getByRole('button', { name: 'Add Key' })).not.toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Edit key' })).not.toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Delete key' })).not.toBeInTheDocument();
		await expect
			.element(page.getByRole('button', { name: 'Delete provider' }))
			.not.toBeInTheDocument();
	});
});
