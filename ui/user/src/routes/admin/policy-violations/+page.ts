import type { PageLoad } from './$types';
import { redirect } from '@sveltejs/kit';

// Message policy violations are now recorded as guardrail events.
export const load: PageLoad = () => {
	throw redirect(307, '/llm-gateway/guardrails/events');
};
