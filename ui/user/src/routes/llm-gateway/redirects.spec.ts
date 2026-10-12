import { load } from './[...path]/+page';
import { legacyGatewayLocation } from './redirects';
import { isRedirect } from '@sveltejs/kit';
import { describe, expect, it } from 'vitest';

function redirectOf(path: string, search = '') {
	try {
		load({
			params: { path },
			url: new URL(`http://localhost/llm-gateway/${path}${search}`)
		} as unknown as Parameters<typeof load>[0]);
	} catch (e) {
		if (isRedirect(e)) return { status: e.status, location: e.location };
		throw e;
	}
	throw new Error('load did not redirect');
}

describe('legacy LLM gateway redirects', () => {
	it.each([
		{ from: '', to: '/models?view=providers' },
		{ from: 'overview', to: '/usage?view=gateway' },
		{ from: 'models', to: '/models?view=models' },
		{ from: 'providers', to: '/models?view=providers' },
		{ from: 'providers/openai', to: '/models/providers/openai' },
		{ from: 'budgets', to: '/models?view=budgets' },
		{ from: 'budgets/new', to: '/models/budgets/new' },
		{ from: 'budgets/b1', to: '/models/budgets/b1' },
		{ from: 'guardrails', to: '/models?view=guardrails' },
		{ from: 'guardrails/rules', to: '/models?view=guardrails' },
		{ from: 'guardrails/rules/r1', to: '/models/guardrails/r1' },
		{ from: 'guardrails/profiles', to: '/guardrails?view=detectors' },
		{ from: 'guardrails/profiles/p1', to: '/guardrails/detectors/p1' },
		{ from: 'guardrails/events', to: '/guardrail-events' },
		{ from: 'routing', to: '/models?view=routing' },
		{ from: 'logs', to: '/audit-logs?view=gateway' },
		{ from: 'settings', to: '/models?view=settings' },
		{ from: 'unknown/page', to: '/models?view=providers' }
	])('sends /llm-gateway/$from to $to', ({ from, to }) => {
		expect(redirectOf(from)).toEqual({ status: 307, location: to });
	});

	it('keeps query parameters, such as the type of a new detector', () => {
		expect(redirectOf('guardrails/profiles/new', '?type=presidio')).toEqual({
			status: 307,
			location: '/guardrails/detectors/new?type=presidio'
		});
	});

	it('replaces an old view parameter on list pages', () => {
		expect(legacyGatewayLocation('providers', new URLSearchParams('view=x&query=open'))).toBe(
			'/models?view=providers&query=open'
		);
	});

	it('encodes provider names', () => {
		expect(legacyGatewayLocation('providers/my provider')).toBe('/models/providers/my%20provider');
	});
});
