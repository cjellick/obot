import { page as appPage } from '$app/state';
import { preparePageData } from '../../tests/helpers/pageData';
import type { PageData } from './$types';
import ModelsPage from './+page.svelte';
import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

// ModelProvidersView reads the admin models context that the Models page sets up, so it is
// rendered through the page on its Model Providers tab.
async function renderModelProvidersView() {
	appPage.url.searchParams.set('view', 'model-providers');
	const data = await preparePageData<PageData>({
		models: [],
		modelProviders: [],
		modelAccessPolicies: [],
		hasAdminAccess: true
	});
	return render(ModelsPage, { data });
}

afterEach(() => {
	appPage.url.searchParams.delete('view');
});

describe('ModelProvidersView', () => {
	it('links admins to LLM gateway provider setup', async () => {
		await renderModelProvidersView();

		const link = page.getByRole('link', { name: /LLM Gateway/ });
		await expect.element(link).toHaveAttribute('href', '/llm-gateway/providers');
		await expect
			.element(
				link.getByText('The providers below are the legacy per-provider integrations.', {
					exact: false
				})
			)
			.toBeInTheDocument();
	});
});
