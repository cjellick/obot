import { Group } from '$lib/services';
import { createMockProfile, createPageData } from '../../tests/helpers/pageData';
import { load } from './+page';
import { describe, expect, it, vi } from 'vitest';

function loadEvents(groups: string[], url = 'http://localhost/guardrail-events') {
	return load({
		fetch: vi.fn(),
		parent: async () => createPageData({ profile: createMockProfile(groups) }),
		url: new URL(url)
	} as unknown as Parameters<NonNullable<typeof load>>[0]);
}

describe('Guardrail events loader', () => {
	it('lets administrators and auditors in', async () => {
		await expect(loadEvents([Group.ADMIN])).resolves.toEqual({});
		await expect(loadEvents([Group.USER, Group.AUDITOR])).resolves.toEqual({});
	});

	it('sends everyone else home', async () => {
		await expect(loadEvents([Group.USER])).rejects.toMatchObject({
			status: 307,
			location: '/'
		});
	});
});
