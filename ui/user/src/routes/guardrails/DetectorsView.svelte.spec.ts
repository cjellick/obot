import { page as appPage } from '$app/state';
import { Group } from '$lib/services';
import { goto } from '$lib/url';
import { createMockProfile, preparePageData } from '../../tests/helpers/pageData';
import { worker } from '../../tests/mocks/worker';
import DetectorsView from './DetectorsView.svelte';
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
	},
	{
		type: 'presidio',
		label: 'Microsoft Presidio',
		description: 'Detects personal data with a self-hosted Presidio Analyzer.',
		category: 'Integrations',
		fields: [],
		redacts: true
	}
];

function mockGuardrails(detectors: unknown[]) {
	worker.use(
		http.get('/api/llm-gateway/guardrails/profiles', () => HttpResponse.json({ items: detectors })),
		http.get('/api/llm-gateway/guardrails/providers', () => HttpResponse.json({ items: providers }))
	);
}

const regexDetector = { id: 'p1', name: 'Common PII', type: 'regex', config: {} };
const presidioDetector = {
	id: 'p2',
	name: 'Presidio',
	type: 'presidio',
	config: {},
	error: 'Analyzer URL is required'
};

async function renderView(groups: string[] = [Group.ADMIN]) {
	await preparePageData({ profile: createMockProfile(groups) });
	return render(DetectorsView);
}

afterEach(() => {
	appPage.url.searchParams.delete('query');
});

describe('Guardrail detectors view', () => {
	it('lists detectors with their type and status', async () => {
		mockGuardrails([regexDetector, presidioDetector]);
		await renderView();

		await expect.element(page.getByText('Common PII')).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: 'Custom Regex' })).toBeInTheDocument();
		await expect.element(page.getByText('Analyzer URL is required')).toBeInTheDocument();
		await expect.element(page.getByText('Ready', { exact: true })).toBeInTheDocument();
	});

	it('points to where rules are configured', async () => {
		mockGuardrails([regexDetector]);
		await renderView();

		await expect
			.element(page.getByRole('link', { name: 'Models → Guardrails' }))
			.toHaveAttribute('href', '/models?view=guardrails');
		await expect
			.element(page.getByRole('link', { name: 'MCP Servers → Filters' }))
			.toHaveAttribute('href', '/mcp-servers?view=filters');
	});

	it('filters detectors by the search query', async () => {
		mockGuardrails([regexDetector, presidioDetector]);
		appPage.url.searchParams.set('query', 'presidio');
		await renderView();

		await expect
			.element(page.getByRole('cell', { name: 'Presidio', exact: true }))
			.toBeInTheDocument();
		await expect.element(page.getByText('Common PII')).not.toBeInTheDocument();
	});

	it('shows an empty state that points to the rule pages', async () => {
		mockGuardrails([]);
		await renderView();

		await expect
			.element(page.getByRole('heading', { name: 'No guardrail detectors' }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('link', { name: 'Models → Guardrails' }))
			.toBeInTheDocument();
	});

	it('picks a provider type from categorized cards to add a detector', async () => {
		mockGuardrails([]);
		const view = await renderView();
		await expect
			.element(page.getByRole('heading', { name: 'No guardrail detectors' }))
			.toBeInTheDocument();

		view.component.openTypePicker();
		await expect.element(page.getByText('Integrations', { exact: true })).toBeInTheDocument();
		await page.getByRole('button', { name: /Microsoft Presidio/ }).click();

		expect(goto).toHaveBeenCalledWith('/guardrails/detectors/new?type=presidio');
	});

	it('shows why a detector in use cannot be deleted', async () => {
		mockGuardrails([regexDetector]);
		worker.use(
			http.delete('/api/llm-gateway/guardrails/profiles/p1', () =>
				HttpResponse.json(
					{ error: 'profile is used by rule "Block PII"; remove it from the rule first' },
					{ status: 409 }
				)
			)
		);
		await renderView();

		await page.getByRole('button', { name: 'Delete detector' }).click();
		await page.getByRole('button', { name: "Yes, I'm sure" }).click();

		await expect
			.element(page.getByText('profile is used by rule "Block PII"; remove it from the rule first'))
			.toBeInTheDocument();
	});

	it('hides delete from auditors', async () => {
		mockGuardrails([regexDetector]);
		await renderView([Group.USER, Group.AUDITOR]);

		await expect.element(page.getByText('Common PII')).toBeInTheDocument();
		await expect
			.element(page.getByRole('button', { name: 'Delete detector' }))
			.not.toBeInTheDocument();
	});
});
