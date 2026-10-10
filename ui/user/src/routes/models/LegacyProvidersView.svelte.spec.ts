import { worker } from '../../tests/mocks/worker';
import { readyStatus, renderModelsTab, resetModelsTab } from './testing';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it } from 'vitest';
import { page } from 'vitest/browser';

// LegacyProvidersView reads the admin models context that the Models page sets up, so it is
// rendered through the page on its Providers tab.
function mockGateway() {
	worker.use(
		http.get('/api/llm-gateway/status', () => HttpResponse.json(readyStatus)),
		http.get('/api/llm-gateway/admin/providers', () => HttpResponse.json({ providers: [] }))
	);
}

afterEach(resetModelsTab);

describe('LegacyProvidersView', () => {
	it('sits in the Providers tab without a separate gateway link', async () => {
		mockGateway();
		await renderModelsTab('providers');

		// The gateway provider list has loaded.
		await expect.element(page.getByRole('heading', { name: 'No providers' })).toBeInTheDocument();
		await expect
			.element(page.getByRole('heading', { name: 'Legacy providers' }))
			.toBeInTheDocument();
		await expect.element(page.getByRole('link', { name: /LLM Gateway/ })).not.toBeInTheDocument();
	});
});
