import { page as appPage } from '$app/state';
import { Group } from '$lib/services';
import { goto } from '$lib/url';
import { createMockProfile, preparePageData } from '../../tests/helpers/pageData';
import { worker } from '../../tests/mocks/worker';
import GuardrailsPage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

vi.mock(import('$lib/url'), async (importOriginal) => ({
	...(await importOriginal()),
	goto: vi.fn()
}));

const providers = [
	{
		type: 'regex',
		label: 'Custom Regex',
		description: 'Matches content against regular expressions.',
		category: 'Built-in',
		fields: [],
		redacts: true
	}
];

function mockApis() {
	worker.use(
		http.get('/api/llm-gateway/guardrails/profiles', () =>
			HttpResponse.json({ items: [{ id: 'p1', name: 'Common PII', type: 'regex', config: {} }] })
		),
		http.get('/api/llm-gateway/guardrails/providers', () =>
			HttpResponse.json({ items: providers })
		),
		http.get('/api/llm-gateway/guardrails/events', () =>
			HttpResponse.json({ items: [], total: 0 })
		),
		http.get('/api/llm-gateway/guardrails/rules', () => HttpResponse.json({ items: [] }))
	);
}

async function renderGuardrailsPage(view: string, groups: string[] = [Group.ADMIN]) {
	appPage.url.searchParams.set('view', view);
	mockApis();
	await preparePageData({ profile: createMockProfile(groups) });
	return render(GuardrailsPage);
}

afterEach(() => {
	appPage.url.searchParams.delete('view');
});

describe('Guardrails page', () => {
	it('shows the detectors', async () => {
		await renderGuardrailsPage('detectors');

		await expect
			.element(page.getByRole('heading', { name: 'Guardrail Detectors' }))
			.toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Events' })).not.toBeInTheDocument();
		await expect.element(page.getByText('Common PII')).toBeInTheDocument();
		await expect
			.element(page.getByRole('link', { name: 'Models → Guardrails' }))
			.toHaveAttribute('href', '/models?view=guardrails');
		await expect
			.element(page.getByRole('link', { name: 'MCP Servers → Filters' }))
			.toHaveAttribute('href', '/mcp-servers?view=filters');
	});

	it('adds a detector by picking its type', async () => {
		await renderGuardrailsPage('detectors');

		await page.getByRole('button', { name: 'Add Detector' }).click();
		await page.getByRole('button', { name: /Custom Regex/ }).click();

		expect(goto).toHaveBeenCalledWith('/guardrails/detectors/new?type=regex');
	});

	it('hides Add Detector from auditors', async () => {
		await renderGuardrailsPage('detectors', [Group.USER, Group.AUDITOR]);

		await expect.element(page.getByText('Common PII')).toBeInTheDocument();
		await expect
			.element(page.getByRole('button', { name: 'Add Detector' }))
			.not.toBeInTheDocument();
	});
});
