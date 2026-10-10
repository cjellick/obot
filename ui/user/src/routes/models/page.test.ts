import { AdminService, Group } from '$lib/services';
import { createMockProfile, createPageData } from '../../tests/helpers/pageData';
import { load as legacyAdminLoad } from '../admin/model-providers/+page';
import { load } from './+page';
import { isRedirect } from '@sveltejs/kit';
import { afterEach, describe, expect, it, vi } from 'vitest';

function loadModels(search: string, groups = [Group.ADMIN]) {
	return load({
		fetch: vi.fn(),
		parent: async () => createPageData({ profile: createMockProfile(groups), models: [] }),
		url: new URL(`http://localhost/models${search}`)
	} as unknown as Parameters<NonNullable<typeof load>>[0]);
}

async function redirectOf(run: () => unknown) {
	try {
		await run();
	} catch (e) {
		if (isRedirect(e)) return { status: e.status, location: e.location };
		throw e;
	}
	throw new Error('load did not redirect');
}

describe('Models page loader', () => {
	afterEach(() => vi.restoreAllMocks());

	it('sends the old model providers tab to the Providers tab, keeping other params', async () => {
		expect(await redirectOf(() => loadModels('?view=model-providers&query=open'))).toEqual({
			status: 307,
			location: '/models?view=providers&query=open'
		});
	});

	it('sends the old admin model providers page to the Providers tab', async () => {
		const location = (
			await redirectOf(() =>
				legacyAdminLoad({
					url: new URL('http://localhost/admin/model-providers')
				} as unknown as Parameters<typeof legacyAdminLoad>[0])
			)
		).location;
		expect(location.startsWith('/models?view=providers')).toBe(true);
	});

	it('loads the legacy model providers for admins on the Providers tab', async () => {
		const list = vi.spyOn(AdminService, 'listModelProviders').mockResolvedValue([]);

		const result = await loadModels('?view=providers');

		expect(list).toHaveBeenCalledOnce();
		expect(result).toMatchObject({ modelProviders: [], hasAdminAccess: true });
	});

	it('does not load admin data for users on admin tabs', async () => {
		const list = vi.spyOn(AdminService, 'listModelProviders');

		const result = await loadModels('?view=providers', [Group.USER]);

		expect(list).not.toHaveBeenCalled();
		expect(result).toMatchObject({ hasAdminAccess: false });
	});
});
