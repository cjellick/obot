import '$lib/services';
import {
	CAPABILITY_LABELS,
	CONTENT_LABELS,
	type CustomLabel,
	type LabelInventory
} from '$lib/services/provenance';
import { preparePageData } from '../../../tests/helpers/pageData';
import { worker } from '../../../tests/mocks/worker';
import LabelsOverview from './LabelsOverview.svelte';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

const atlas: CustomLabel = {
	name: 'project-atlas',
	description: 'Anything about Project Atlas',
	ai: true,
	confidential: true,
	patterns: [{ type: 'regex', value: '\\bATL-\\d{4}\\b' }],
	sources: [{ type: 'catalog-entry', id: 'salesforce', name: 'Salesforce', tool: 'query' }]
};

function inventory(customLabels: CustomLabel[] = []): LabelInventory {
	return {
		resources: [
			{
				resourceType: 'catalog-entry',
				resourceID: 'salesforce',
				displayName: 'Salesforce',
				toolCount: 1,
				reviewedToolCount: 1,
				pendingToolCount: 0,
				staleToolCount: 0,
				labels: []
			}
		],
		observedTools: [],
		capabilities: CAPABILITY_LABELS,
		contentLabels: CONTENT_LABELS,
		customLabels,
		contentTagging: { enabled: true, mode: 'async' },
		supervisorEnabled: true
	};
}

async function openLibrary(customLabels: CustomLabel[] = []) {
	worker.use(
		http.get('/api/provenance/label-inventory', () => HttpResponse.json(inventory(customLabels)))
	);
	await preparePageData();
	render(LabelsOverview);
	await page.getByRole('tab', { name: 'Label library' }).click();
	return page.getByRole('region', { name: 'Custom labels' });
}

describe('Custom label editor', () => {
	it('creates a label with AI, a keyword and a source', async () => {
		let body: CustomLabel | undefined;
		worker.use(
			http.post('/api/provenance/custom-labels', async ({ request }) => {
				body = (await request.json()) as CustomLabel;
				return HttpResponse.json({ ...body, createdAt: '2026-11-10T00:00:00Z' }, { status: 201 });
			})
		);
		const section = await openLibrary();
		await expect.element(section.getByText('No custom labels yet.')).toBeVisible();
		await section.getByRole('button', { name: 'New custom label' }).click();

		const form = page.getByRole('form', { name: 'New custom label' });
		const create = form.getByRole('button', { name: 'Create label' });
		await form.getByRole('textbox', { name: 'Name' }).fill('pii');
		await expect.element(form.getByText('That is a built-in label.')).toBeVisible();
		await form.getByRole('textbox', { name: 'Name' }).fill('board-minutes');
		await form.getByRole('textbox', { name: 'Description' }).fill('Minutes of board meetings');
		await expect.element(create).toBeDisabled();
		await expect
			.element(form.getByText('Choose at least one detector: AI, a pattern or a source.'))
			.toBeVisible();

		await form.getByRole('checkbox', { name: /AI tagger/ }).click();
		await form.getByRole('button', { name: 'Add pattern' }).click();
		await form.getByRole('textbox', { name: 'Pattern', exact: true }).fill(' board vote ');
		await form.getByRole('button', { name: 'Add source' }).click();
		await form.getByRole('combobox', { name: 'Server ID' }).fill('salesforce');
		await expect.element(create).toBeEnabled();
		await create.click();

		await expect.poll(() => body).toBeDefined();
		expect(body).toMatchObject({
			name: 'board-minutes',
			description: 'Minutes of board meetings',
			ai: true,
			confidential: true,
			patterns: [{ type: 'keyword', value: 'board vote' }],
			sources: [{ type: 'catalog-entry', id: 'salesforce', name: 'Salesforce' }]
		});
		await expect.element(page.getByRole('form')).not.toBeInTheDocument();
		await expect.element(section.getByText('board-minutes', { exact: true })).toBeVisible();
		await expect.element(section.getByText('1 patterns')).toBeVisible();
	});

	it('lists, edits and deletes an existing label', async () => {
		let updated: CustomLabel | undefined;
		let deleted = '';
		worker.use(
			http.put('/api/provenance/custom-labels/:name', async ({ request }) => {
				updated = (await request.json()) as CustomLabel;
				return HttpResponse.json(updated);
			}),
			http.delete('/api/provenance/custom-labels/:name', ({ params }) => {
				deleted = String(params.name);
				return new HttpResponse(null, { status: 204 });
			})
		);
		const section = await openLibrary([atlas]);
		await expect.element(section.getByText('project-atlas', { exact: true })).toBeVisible();
		await expect.element(section.getByText('Anything about Project Atlas')).toBeVisible();
		await expect.element(section.getByText(/Sources: MCP Salesforce, tool query/)).toBeVisible();

		await section.getByRole('button', { name: 'Edit project-atlas' }).click();
		const form = page.getByRole('form', { name: 'Edit project-atlas' });
		await expect.element(form.getByRole('textbox', { name: 'Name' })).toBeDisabled();
		await form.getByRole('textbox', { name: 'Description' }).fill('Project Atlas launch plans');
		await form.getByRole('button', { name: 'Save label' }).click();
		await expect.poll(() => updated?.description).toBe('Project Atlas launch plans');
		await expect.element(section.getByText('Project Atlas launch plans')).toBeVisible();

		await section.getByRole('button', { name: 'Delete project-atlas' }).click();
		await page.getByRole('button', { name: "Yes, I'm sure" }).click();
		await expect.poll(() => deleted).toBe('project-atlas');
		await expect.element(section.getByText('No custom labels yet.')).toBeVisible();
	});

	it('tests patterns against sample text', async () => {
		worker.use(
			http.post('/api/provenance/custom-labels/test', () =>
				HttpResponse.json({
					matched: true,
					hits: [
						{
							detector: 'pattern',
							detail: 'matched pattern /\\bATL-\\d{4}\\b/',
							matches: ['ATL-2041']
						}
					]
				})
			)
		);
		const section = await openLibrary([atlas]);
		await section.getByRole('button', { name: 'Edit project-atlas' }).click();
		const form = page.getByRole('form', { name: 'Edit project-atlas' });
		await form.getByText('Test patterns').click();
		await form.getByRole('textbox', { name: 'Paste sample text' }).fill('Ticket ATL-2041');
		await form.getByRole('button', { name: 'Run test' }).click();
		await expect.element(form.getByText('The label would be applied.')).toBeVisible();
		await expect.element(form.getByText('ATL-2041')).toBeVisible();
	});
});
