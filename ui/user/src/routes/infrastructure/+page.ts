import type { PageLoad } from './$types';
import { redirect } from '@sveltejs/kit';

// Infrastructure is for administrators.
export const load: PageLoad = async ({ parent }) => {
	const { profile } = await parent();
	if (!profile?.isAdmin?.()) {
		throw redirect(307, '/');
	}
	return {};
};
