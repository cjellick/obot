import { goto } from '$lib/url';
import { preparePageData } from '../../../../tests/helpers/pageData';
import { worker } from '../../../../tests/mocks/worker';
import ProfilesPage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
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

function mockGuardrails(profiles: unknown[]) {
	worker.use(
		http.get('/api/llm-gateway/guardrails/profiles', () => HttpResponse.json({ items: profiles })),
		http.get('/api/llm-gateway/guardrails/providers', () => HttpResponse.json({ items: providers }))
	);
}

const regexProfile = { id: 'p1', name: 'Common PII', type: 'regex', config: {} };
const presidioProfile = {
	id: 'p2',
	name: 'Presidio',
	type: 'presidio',
	config: {},
	error: 'Analyzer URL is required'
};

describe('Guardrail profiles page', () => {
	it('lists profiles with their type and status', async () => {
		mockGuardrails([regexProfile, presidioProfile]);
		await preparePageData();
		render(ProfilesPage);

		await expect.element(page.getByText('Common PII')).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: 'Custom Regex' })).toBeInTheDocument();
		await expect.element(page.getByText('Analyzer URL is required')).toBeInTheDocument();
		await expect.element(page.getByText('Ready', { exact: true })).toBeInTheDocument();
	});

	it('picks a provider type from categorized cards to add a profile', async () => {
		mockGuardrails([]);
		await preparePageData();
		render(ProfilesPage);

		await expect
			.element(page.getByRole('heading', { name: 'No guardrail profiles' }))
			.toBeInTheDocument();
		await page.getByRole('button', { name: 'Add Profile' }).click();
		await expect.element(page.getByText('Integrations', { exact: true })).toBeInTheDocument();
		await page.getByRole('button', { name: /Microsoft Presidio/ }).click();

		expect(goto).toHaveBeenCalledWith('/llm-gateway/guardrails/profiles/new?type=presidio');
	});

	it('shows why a profile in use cannot be deleted', async () => {
		mockGuardrails([regexProfile]);
		worker.use(
			http.delete('/api/llm-gateway/guardrails/profiles/p1', () =>
				HttpResponse.json(
					{ error: 'profile is used by rule "Block PII"; remove it from the rule first' },
					{ status: 409 }
				)
			)
		);
		await preparePageData();
		render(ProfilesPage);

		await page.getByRole('button', { name: 'Delete profile' }).click();
		await page.getByRole('button', { name: "Yes, I'm sure" }).click();

		await expect
			.element(page.getByText('profile is used by rule "Block PII"; remove it from the rule first'))
			.toBeInTheDocument();
	});
});
