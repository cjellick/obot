import { legacyGatewayLocation } from '../redirects';
import type { PageLoad } from './$types';
import { redirect } from '@sveltejs/kit';

export const load: PageLoad = ({ params, url }) => {
	throw redirect(307, legacyGatewayLocation(params.path, url.searchParams));
};
