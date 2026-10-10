import GuardrailsTabs from './GuardrailsTabs.svelte';
import { describe, expect, it } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

describe('GuardrailsTabs', () => {
	it('links to every guardrails section', async () => {
		render(GuardrailsTabs, { active: 'rules' });

		const nav = page.getByRole('navigation', { name: 'Guardrails sections' });
		await expect
			.element(nav.getByRole('link', { name: 'Rules' }))
			.toHaveAttribute('href', '/llm-gateway/guardrails');
		await expect
			.element(nav.getByRole('link', { name: 'Profiles' }))
			.toHaveAttribute('href', '/llm-gateway/guardrails/profiles');
		await expect
			.element(nav.getByRole('link', { name: 'Events' }))
			.toHaveAttribute('href', '/llm-gateway/guardrails/events');
	});

	it('marks only the active section as the current page', async () => {
		render(GuardrailsTabs, { active: 'profiles' });

		await expect
			.element(page.getByRole('link', { name: 'Profiles' }))
			.toHaveAttribute('aria-current', 'page');
		await expect
			.element(page.getByRole('link', { name: 'Rules' }))
			.not.toHaveAttribute('aria-current');
		await expect
			.element(page.getByRole('link', { name: 'Events' }))
			.not.toHaveAttribute('aria-current');
	});
});
