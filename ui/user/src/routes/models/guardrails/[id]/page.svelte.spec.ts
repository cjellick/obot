import { page as appPage } from '$app/state';
import { goto } from '$lib/url';
import { preparePageData } from '../../../../tests/helpers/pageData';
import { worker } from '../../../../tests/mocks/worker';
import RulePage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

vi.mock(import('$lib/url'), async (importOriginal) => ({
	...(await importOriginal()),
	goto: vi.fn()
}));

function mockProfilesAndProviders() {
	worker.use(
		http.get('/api/llm-gateway/guardrails/profiles', () =>
			HttpResponse.json({
				items: [
					{ id: 'p1', name: 'Common PII', type: 'regex', config: {}, configuredSecrets: [] },
					{ id: 'p2', name: 'Content safety', type: 'azure-content-safety', config: {} }
				]
			})
		),
		http.get('/api/llm-gateway/guardrails/providers', () =>
			HttpResponse.json({
				items: [
					{
						type: 'regex',
						label: 'Custom Regex',
						description: '',
						category: 'Built-in',
						fields: [],
						redacts: true
					},
					{
						type: 'azure-content-safety',
						label: 'Azure AI Content Safety',
						description: '',
						category: 'Integrations',
						fields: [],
						redacts: false
					}
				]
			})
		),
		http.post('/api/llm-gateway/guardrails/validate-condition', async ({ request }) => {
			const { condition } = (await request.json()) as { condition: string };
			return HttpResponse.json(
				condition.includes('bogus')
					? { valid: false, error: "undeclared reference to 'bogus'" }
					: { valid: true }
			);
		})
	);
}

afterEach(() => {
	delete (appPage.params as Record<string, string>).id;
});

describe('Model guardrail rule editor', () => {
	it('creates a rule with the selected detectors and condition', async () => {
		mockProfilesAndProviders();
		const created = vi.fn();
		worker.use(
			http.post('/api/llm-gateway/guardrails/rules', async ({ request }) => {
				created(await request.json());
				return HttpResponse.json({ id: 'r1' });
			})
		);
		await preparePageData();
		render(RulePage);

		const save = page.getByRole('button', { name: 'Save', exact: true });
		await page.getByLabelText('Name', { exact: true }).fill('Block PII');
		await expect.element(save).toBeDisabled();
		await page.getByText('Common PII').click();
		await page.getByLabelText('Condition').fill('model.startsWith("openai/")');
		await expect.element(save).toBeEnabled();
		await save.click();

		await vi.waitFor(() => expect(created).toHaveBeenCalledOnce());
		expect(created).toHaveBeenCalledWith(
			expect.objectContaining({
				name: 'Block PII',
				enabled: true,
				target: 'llm',
				applyTo: 'both',
				action: 'block',
				condition: 'model.startsWith("openai/")',
				samplingRate: 100,
				inputScope: 'latest',
				failClosed: false,
				profileIDs: ['p1'],
				subjects: []
			})
		);
		await vi.waitFor(() => expect(goto).toHaveBeenCalledWith('/models?view=guardrails'));
	});

	it('shows condition errors as the condition is typed', async () => {
		mockProfilesAndProviders();
		await preparePageData();
		render(RulePage);

		await page.getByLabelText('Name', { exact: true }).fill('Bad condition');
		await page.getByText('Common PII').click();
		await page.getByLabelText('Condition').fill('bogus == 1');

		await expect
			.element(page.getByRole('alert'))
			.toHaveTextContent("undeclared reference to 'bogus'");
		await expect.element(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
	});

	it('only offers model rules, with model condition variables', async () => {
		mockProfilesAndProviders();
		await preparePageData();
		render(RulePage);

		await expect.element(page.getByText('provider', { exact: true })).toBeInTheDocument();
		await expect.element(page.getByText('mcp_tool', { exact: true })).not.toBeInTheDocument();
		await expect.element(page.getByLabelText('Target')).not.toBeInTheDocument();
		await expect
			.element(page.getByRole('link', { name: 'Create a detector' }))
			.toHaveAttribute('href', '/guardrails?view=detectors');
	});

	it('saves an existing rule as a model rule', async () => {
		mockProfilesAndProviders();
		mockRedactingRule();
		const updated = vi.fn();
		worker.use(
			http.put('/api/llm-gateway/guardrails/rules/:id', async ({ params, request }) => {
				updated(params.id, await request.json());
				return HttpResponse.json({ id: 'r2' });
			})
		);
		await preparePageData();
		render(RulePage);

		await expect
			.element(page.getByLabelText('Name', { exact: true }))
			.toHaveValue('Redact everything');
		await page.getByRole('combobox', { name: 'Apply to' }).click();
		await page.getByRole('button', { name: 'Input & output', exact: true }).click();
		await page.getByRole('button', { name: 'Save', exact: true }).click();

		await vi.waitFor(() => expect(updated).toHaveBeenCalledOnce());
		expect(updated).toHaveBeenCalledWith(
			'r2',
			expect.objectContaining({ target: 'llm', applyTo: 'both', action: 'redact' })
		);
	});

	function mockRedactingRule() {
		(appPage.params as Record<string, string>).id = 'r2';
		worker.use(
			http.get('/api/llm-gateway/guardrails/rules/r2', () =>
				HttpResponse.json({
					id: 'r2',
					name: 'Redact everything',
					enabled: true,
					target: 'llm',
					applyTo: 'output',
					action: 'redact',
					samplingRate: 50,
					failClosed: true,
					profileIDs: ['p1', 'p2'],
					subjects: [{ type: 'everyone' }]
				})
			)
		);
	}

	it('loads an existing rule', async () => {
		mockProfilesAndProviders();
		mockRedactingRule();
		await preparePageData();
		render(RulePage);

		await expect
			.element(page.getByLabelText('Name', { exact: true }))
			.toHaveValue('Redact everything');
		await expect.element(page.getByLabelText('Sampling rate (%)')).toHaveValue(50);
		await expect.element(page.getByText('All Obot Users')).toBeInTheDocument();
		// Input scope only matters when the rule checks input.
		await expect.element(page.getByLabelText('Input scope')).not.toBeInTheDocument();
	});

	it('warns when a redacting rule uses a profile that cannot redact', async () => {
		mockProfilesAndProviders();
		mockRedactingRule();
		await preparePageData();
		render(RulePage);

		const warning = page.getByText('locate findings for redaction', { exact: false });
		await expect.element(warning).toBeInTheDocument();
		expect(warning.element().textContent?.replace(/\s+/g, ' ')).toContain(
			'Content safety uses a provider'
		);
		await page.getByRole('combobox', { name: 'Action' }).click();
		await page.getByRole('button', { name: 'Block', exact: true }).click();
		await expect.element(warning).not.toBeInTheDocument();
	});
});
