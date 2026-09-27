import type { PageLoad } from './$types';
import { redirect } from '@sveltejs/kit';

// Message policies are now guardrails: each became an LLM Judge profile and a blocking rule.
export const load: PageLoad = () => {
	throw redirect(307, '/llm-gateway/guardrails');
};
