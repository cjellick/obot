import { Group } from '$lib/services';
import { createMockProfile, createPageData } from '../../tests/helpers/pageData';
import { load } from './+page';
import { describe, expect, it, vi } from 'vitest';

function loadGuardrails(groups: string[]) {
	return load({
		fetch: vi.fn(),
		parent: async () => createPageData({ profile: createMockProfile(groups) }),
		url: new URL('http://localhost/guardrails')
	} as unknown as Parameters<NonNullable<typeof load>>[0]);
}

describe('Guardrails loader', () => {
	it('lets administrators and auditors in', async () => {
		await expect(loadGuardrails([Group.ADMIN])).resolves.toEqual({});
		await expect(loadGuardrails([Group.USER, Group.AUDITOR])).resolves.toEqual({});
	});

	it('sends everyone else home', async () => {
		await expect(loadGuardrails([Group.USER])).rejects.toMatchObject({
			status: 307,
			location: '/'
		});
	});
});
