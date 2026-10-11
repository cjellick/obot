import { browser } from '$app/environment';
import { handleRouteError } from '$lib/errors';
import { AdminService, UserService, type Model } from '$lib/services';
import type { ModelAccessPolicy } from '$lib/services/admin/types';
import accessibleModels, { filterAccessibleModels } from '$lib/stores/accessibleModels.svelte';
import type { PageLoad } from './$types';
import { redirect } from '@sveltejs/kit';

const views = new Set([
	'models',
	'providers',
	'routing',
	'budgets',
	'guardrails',
	'access-policies',
	'settings'
]);

// Older links point at the model providers tab, which is now the Providers tab.
const viewAliases: Record<string, string> = { 'model-providers': 'providers' };

export const load: PageLoad = async ({ fetch, parent, url }) => {
	const alias = viewAliases[url.searchParams.get('view') ?? ''];
	if (alias) {
		const searchParams = new URLSearchParams(url.searchParams);
		searchParams.set('view', alias);
		throw redirect(307, `${url.pathname}?${searchParams}`);
	}

	const { profile, models: initialModels } = await parent();
	const hasAdminAccess = profile.hasAdminAccess?.() ?? false;
	const requestedView = url.searchParams.get('view');
	const view =
		requestedView && views.has(requestedView) && (hasAdminAccess || requestedView === 'models')
			? requestedView
			: 'models';

	let models: Model[] = [];
	let modelAccessPolicies: ModelAccessPolicy[] = [];

	if (view === 'models') {
		try {
			const response =
				browser && accessibleModels.initialized && accessibleModels.current.length > 0
					? accessibleModels.current
					: (initialModels ?? (await UserService.listModels({ fetch })));

			models = filterAccessibleModels(response ?? []);

			if (browser) {
				accessibleModels.set(models);
			}
		} catch (err) {
			handleRouteError(err, '/models', profile);
		}
	} else if (hasAdminAccess && view === 'access-policies') {
		try {
			modelAccessPolicies = await AdminService.listModelAccessPolicies({ fetch });
		} catch (err) {
			handleRouteError(err, '/models', profile);
		}
	}

	return {
		models,
		modelAccessPolicies,
		hasAdminAccess
	};
};
