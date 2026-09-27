import { load as policyViolationsLoad } from '../policy-violations/+page';
import { load as messagePoliciesLoad } from './+page';
import { load as messagePolicyLoad } from './[id]/+page';
import { isRedirect } from '@sveltejs/kit';
import { describe, expect, it } from 'vitest';

function redirectOf(load: () => unknown) {
	try {
		load();
	} catch (e) {
		if (isRedirect(e)) return { status: e.status, location: e.location };
		throw e;
	}
	throw new Error('load did not redirect');
}

describe('message policy redirects', () => {
	it('sends message policies to guardrails', () => {
		expect(redirectOf(messagePoliciesLoad as () => unknown)).toEqual({
			status: 307,
			location: '/llm-gateway/guardrails'
		});
		expect(redirectOf(messagePolicyLoad as () => unknown)).toEqual({
			status: 307,
			location: '/llm-gateway/guardrails'
		});
	});

	it('sends policy violations to guardrail events', () => {
		expect(redirectOf(policyViolationsLoad as () => unknown)).toEqual({
			status: 307,
			location: '/llm-gateway/guardrails/events'
		});
	});
});
