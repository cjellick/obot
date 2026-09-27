import { page as appPage } from '$app/state';
import { goto } from '$lib/url';
import { preparePageData } from '../../../../../tests/helpers/pageData';
import { worker } from '../../../../../tests/mocks/worker';
import ProfilePage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

vi.mock(import('$lib/url'), async (importOriginal) => ({
	...(await importOriginal()),
	goto: vi.fn()
}));

const presidio = {
	type: 'presidio',
	label: 'Microsoft Presidio',
	description: 'Detects personal data with a self-hosted Presidio Analyzer.',
	category: 'Integrations',
	redacts: true,
	fields: [
		{ name: 'analyzerURL', label: 'Analyzer URL', type: 'text', required: true },
		{ name: 'entities', label: 'Entities', type: 'list' },
		{ name: 'scoreThreshold', label: 'Minimum score', type: 'number', default: 0.5 },
		{ name: 'apiKey', label: 'API key', type: 'password', sensitive: true }
	]
};

function mockProviders() {
	worker.use(
		http.get('/api/llm-gateway/guardrails/providers', () =>
			HttpResponse.json({ items: [presidio] })
		)
	);
}

afterEach(() => {
	delete (appPage.params as Record<string, string>).id;
	appPage.url.searchParams.delete('type');
});

describe('Guardrail profile editor', () => {
	it('creates a profile from the provider fields, then enables testing', async () => {
		mockProviders();
		appPage.url.searchParams.set('type', 'presidio');
		const created = vi.fn();
		worker.use(
			http.post('/api/llm-gateway/guardrails/profiles', async ({ request }) => {
				const body = (await request.json()) as Record<string, unknown>;
				created(body);
				return HttpResponse.json({ ...body, id: 'p1', config: {}, configuredSecrets: ['apiKey'] });
			})
		);
		await preparePageData();
		render(ProfilePage);

		await expect
			.element(page.getByLabelText('Name', { exact: true }))
			.toHaveValue('Microsoft Presidio');
		await expect.element(page.getByText('Save the profile to test it.')).toBeInTheDocument();
		await expect.element(page.getByLabelText('Sample text')).toBeDisabled();

		const save = page.getByRole('button', { name: 'Save', exact: true });
		await expect.element(save).toBeDisabled();
		await page.getByLabelText(/^Analyzer URL/).fill('http://presidio:3000');
		await page.getByLabelText('Entities').fill('EMAIL_ADDRESS\nUS_SSN\n');
		await page.getByLabelText('API key').fill('secret');
		await save.click();

		await vi.waitFor(() => expect(created).toHaveBeenCalledOnce());
		expect(created).toHaveBeenCalledWith({
			name: 'Microsoft Presidio',
			description: '',
			type: 'presidio',
			config: {
				analyzerURL: 'http://presidio:3000',
				entities: ['EMAIL_ADDRESS', 'US_SSN'],
				scoreThreshold: 0.5,
				apiKey: 'secret'
			}
		});
		await vi.waitFor(() =>
			expect(goto).toHaveBeenCalledWith('/llm-gateway/guardrails/profiles/p1', {
				replaceState: true
			})
		);
		await expect.element(page.getByLabelText('Sample text')).toBeEnabled();
	});

	it('keeps configured secrets when left blank and runs a test', async () => {
		mockProviders();
		(appPage.params as Record<string, string>).id = 'p1';
		const updated = vi.fn();
		const tested = vi.fn();
		worker.use(
			http.get('/api/llm-gateway/guardrails/profiles/p1', () =>
				HttpResponse.json({
					id: 'p1',
					name: 'PII',
					type: 'presidio',
					config: { analyzerURL: 'http://presidio:3000', entities: ['EMAIL_ADDRESS'] },
					configuredSecrets: ['apiKey']
				})
			),
			http.put('/api/llm-gateway/guardrails/profiles/p1', async ({ request }) => {
				const body = (await request.json()) as Record<string, unknown>;
				updated(body);
				return HttpResponse.json({ ...body, id: 'p1', configuredSecrets: ['apiKey'] });
			}),
			http.post('/api/llm-gateway/guardrails/profiles/p1/test', async ({ request }) => {
				tested(await request.json());
				return HttpResponse.json({
					findings: [{ category: 'EMAIL_ADDRESS', count: 2 }],
					redactedText: 'Mail [EMAIL_ADDRESS] or [EMAIL_ADDRESS]',
					canRedact: true
				});
			})
		);
		await preparePageData();
		render(ProfilePage);

		await expect.element(page.getByLabelText('Entities')).toHaveValue('EMAIL_ADDRESS');
		await expect
			.element(page.getByPlaceholder('Configured — leave blank to keep'))
			.toBeInTheDocument();

		await page.getByRole('button', { name: 'Save', exact: true }).click();
		await vi.waitFor(() => expect(updated).toHaveBeenCalledOnce());
		expect(updated.mock.calls[0][0].config).not.toHaveProperty('apiKey');

		await page.getByLabelText('Stage').selectOptions('output');
		await page.getByLabelText('Sample text').fill('Mail a@example.com or b@example.com');
		await page.getByRole('button', { name: 'Run test' }).click();

		await vi.waitFor(() =>
			expect(tested).toHaveBeenCalledWith({
				target: 'llm',
				stage: 'output',
				text: 'Mail a@example.com or b@example.com'
			})
		);
		await expect.element(page.getByText('1 finding')).toBeInTheDocument();
		await expect
			.element(page.getByText('Mail [EMAIL_ADDRESS] or [EMAIL_ADDRESS]'))
			.toBeInTheDocument();
	});
});
