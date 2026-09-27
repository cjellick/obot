import { Group } from '$lib/services';
import { createMockProfile, preparePageData } from '../../../tests/helpers/pageData';
import { worker } from '../../../tests/mocks/worker';
import RulesPage from './+page.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

vi.mock(import('$lib/url'), async (importOriginal) => ({
	...(await importOriginal()),
	goto: vi.fn()
}));

const piiRule = {
	id: 'r1',
	name: 'Redact PII',
	enabled: true,
	target: 'llm',
	applyTo: 'both',
	action: 'redact',
	samplingRate: 100,
	failClosed: false,
	profileIDs: ['p1'],
	subjects: []
};

const toolRule = {
	id: 'r2',
	name: 'Tool policy',
	enabled: false,
	target: 'mcp',
	applyTo: 'input',
	action: 'block',
	samplingRate: 100,
	failClosed: true,
	profileIDs: ['p2'],
	subjects: [{ type: 'group', id: 'contractors' }]
};

function mockGuardrails(rules: unknown[] = [piiRule, toolRule]) {
	worker.use(
		http.get('/api/llm-gateway/guardrails/rules', () => HttpResponse.json({ items: rules })),
		http.get('/api/llm-gateway/guardrails/profiles', () =>
			HttpResponse.json({
				items: [
					{ id: 'p1', name: 'Common PII', type: 'regex', config: {}, configuredSecrets: [] },
					{ id: 'p2', name: 'No travel', type: 'llm-judge', config: {}, configuredSecrets: [] }
				]
			})
		)
	);
}

async function renderRulesPage(groups: string[] = [Group.ADMIN]) {
	await preparePageData({ profile: createMockProfile(groups) });
	return render(RulesPage);
}

describe('Guardrail rules page', () => {
	it('lists rules in order with their target, stage, action and profiles', async () => {
		mockGuardrails();
		await renderRulesPage();

		await expect.element(page.getByText('Redact PII')).toBeInTheDocument();
		await expect.element(page.getByText('Common PII')).toBeInTheDocument();
		await expect.element(page.getByText('No travel')).toBeInTheDocument();
		await expect.element(page.getByText('Input & output')).toBeInTheDocument();
		await expect.element(page.getByRole('cell', { name: 'MCP', exact: true })).toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: 'Redact', exact: true }))
			.toBeInTheDocument();
		await expect.element(page.getByRole('link', { name: 'Profiles' })).toBeInTheDocument();
		await expect.element(page.getByRole('link', { name: 'Events' })).toBeInTheDocument();
	});

	it('shows an empty state without rules', async () => {
		mockGuardrails([]);
		await renderRulesPage();

		await expect
			.element(page.getByRole('heading', { name: 'No guardrail rules' }))
			.toBeInTheDocument();
	});

	it('deletes a rule after confirmation', async () => {
		mockGuardrails();
		const deleted = vi.fn();
		worker.use(
			http.delete('/api/llm-gateway/guardrails/rules/:id', ({ params }) => {
				deleted(params.id);
				return new HttpResponse(null, { status: 204 });
			})
		);
		await renderRulesPage();

		await page.getByRole('button', { name: 'Delete rule' }).first().click();
		await page.getByRole('button', { name: "Yes, I'm sure" }).click();

		await vi.waitFor(() => expect(deleted).toHaveBeenCalledWith('r1'));
	});

	it('hides editing actions from auditors', async () => {
		mockGuardrails();
		await renderRulesPage([Group.USER, Group.AUDITOR]);

		await expect.element(page.getByText('Redact PII')).toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Add Rule' })).not.toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Delete rule' })).not.toBeInTheDocument();
	});
});
