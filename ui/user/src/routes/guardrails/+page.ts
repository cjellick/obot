import type { PageLoad } from './$types';
import { redirect } from '@sveltejs/kit';

// Guardrail detectors are admin-only. Events moved to their own page.
export const load: PageLoad = async ({ parent, url }) => {
	if (url.searchParams.get('view') === 'events') {
		const search = new URLSearchParams(url.searchParams);
		search.delete('view');
		const query = search.toString();
		throw redirect(307, '/guardrail-events' + (query ? `?${query}` : ''));
	}
	const { profile } = await parent();
	if (!profile?.hasAdminAccess?.()) {
		throw redirect(307, '/');
	}
	return {};
};
