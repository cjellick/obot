import { Group } from '$lib/services';
import { goto } from '$lib/url';
import { worker } from '../../tests/mocks/worker';
import { renderModelsTab, resetModelsTab } from './testing';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';
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

const travelRule = {
	id: 'r3',
	name: 'No travel advice',
	enabled: false,
	target: 'llm',
	applyTo: 'input',
	action: 'block',
	samplingRate: 100,
	failClosed: true,
	profileIDs: ['p2'],
	subjects: [{ type: 'group', id: 'contractors' }]
};

// MCP guardrails are configured as MCP server filters, so the Models tab leaves them out.
const toolRule = {
	...travelRule,
	id: 'r2',
	name: 'Tool policy',
	target: 'mcp'
};

function mockGuardrails(rules: unknown[] = [piiRule, travelRule, toolRule]) {
	worker.use(
		http.get('/api/llm-gateway/guardrails/rules', () => HttpResponse.json({ items: rules })),
		http.get('/api/llm-gateway/guardrails/profiles', () =>
			HttpResponse.json({
				items: [
					{ id: 'p1', name: 'Common PII', type: 'regex', config: {}, configuredSecrets: [] },
					{ id: 'p2', name: 'Travel judge', type: 'llm-judge', config: {}, configuredSecrets: [] }
				]
			})
		)
	);
}

afterEach(resetModelsTab);

describe('Models guardrails tab', () => {
	it('lists model rules with their stage, action and detectors', async () => {
		mockGuardrails();
		await renderModelsTab('guardrails');

		await expect.element(page.getByText('Redact PII')).toBeInTheDocument();
		await expect.element(page.getByText('Common PII')).toBeInTheDocument();
		await expect.element(page.getByText('Travel judge')).toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: 'Group: contractors', exact: true }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('cell', { name: 'Redact', exact: true }))
			.toBeInTheDocument();
		// Every rule here targets models, so there is no Target column.
		await expect
			.element(page.getByRole('cell', { name: 'LLM', exact: true }))
			.not.toBeInTheDocument();
		await expect.element(page.getByText('Tool policy')).not.toBeInTheDocument();
	});

	it('shows an empty state pointing to detectors and MCP filters', async () => {
		mockGuardrails([toolRule]);
		await renderModelsTab('guardrails');

		await expect
			.element(page.getByRole('heading', { name: 'No model guardrail rules' }))
			.toBeInTheDocument();
		await expect
			.element(page.getByRole('link', { name: 'detectors' }))
			.toHaveAttribute('href', '/guardrails?view=detectors');
		await expect
			.element(page.getByRole('link', { name: 'MCP Servers → Filters' }))
			.toHaveAttribute('href', '/mcp-servers?view=filters');
	});

	it('opens the new rule editor from the nav action', async () => {
		mockGuardrails();
		await renderModelsTab('guardrails');

		await page.getByRole('button', { name: 'Add Rule' }).click();

		expect(goto).toHaveBeenCalledWith('/models/guardrails/new');
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
		await renderModelsTab('guardrails');

		await page
			.getByRole('row', { name: /Redact PII/ })
			.getByRole('button', { name: 'Delete rule' })
			.click();
		await page.getByRole('button', { name: "Yes, I'm sure" }).click();

		await vi.waitFor(() => expect(deleted).toHaveBeenCalledWith('r1'));
	});

	it('hides editing actions from auditors', async () => {
		mockGuardrails();
		await renderModelsTab('guardrails', [Group.USER, Group.AUDITOR]);

		await expect.element(page.getByText('Redact PII')).toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Add Rule' })).not.toBeInTheDocument();
		await expect.element(page.getByRole('button', { name: 'Delete rule' })).not.toBeInTheDocument();
	});
});
