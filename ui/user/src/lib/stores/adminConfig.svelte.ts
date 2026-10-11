import { AdminService, type AuthProvider } from '$lib/services';
import { getStatus } from '$lib/services/llm-gateway/gateway';
import { writable, get } from 'svelte/store';

interface AdminConfig {
	// Whether the LLM gateway has at least one provider configured.
	modelProviderConfigured: boolean;
	authProviderConfigured: boolean;
	authProviders: AuthProvider[];
	eulaAccepted: boolean;
	loading: boolean;
	lastFetched: number | null;
}

const createAdminConfigStore = () => {
	const { subscribe, set, update } = writable<AdminConfig>({
		modelProviderConfigured: false,
		authProviderConfigured: false,
		authProviders: [],
		eulaAccepted: false,
		loading: false,
		lastFetched: null
	});

	let isInitialized = false;

	const fetchData = async (forceRefresh = false) => {
		const now = Date.now();
		const cacheAge = 5 * 60 * 1000; // 5 minutes cache

		// Return cached data if it's fresh and not forcing refresh
		if (!forceRefresh && isInitialized && cacheAge > 0) {
			const currentState = get({ subscribe });
			if (currentState.lastFetched && now - currentState.lastFetched < cacheAge) {
				return;
			}
		}

		update((state) => ({ ...state, loading: true }));

		try {
			const [gatewayStatus, authProviders, eula] = await Promise.all([
				getStatus(),
				AdminService.listAuthProviders(),
				AdminService.getEula()
			]);

			// While the gateway is starting or reloading it can't say whether it has providers.
			// Keep the last known answer (or assume it has, rather than send an admin through
			// setup), and don't cache it, so the next load asks again.
			const previous = get({ subscribe });
			const known = gatewayStatus.ready;
			const modelProviderConfigured = known
				? gatewayStatus.hasProviders
				: previous.lastFetched
					? previous.modelProviderConfigured
					: true;
			const authProviderConfigured = authProviders.some((provider) => provider.configured);
			const eulaAccepted = eula.accepted;

			set({
				modelProviderConfigured,
				authProviderConfigured,
				authProviders,
				eulaAccepted,
				loading: false,
				lastFetched: known ? now : null
			});

			isInitialized = true;
		} catch (error) {
			console.error('Failed to fetch admin config:', error);
			update((state) => ({ ...state, loading: false }));
		}
	};

	const refresh = () => fetchData(true);

	const initialize = () => {
		if (!isInitialized) {
			fetchData();
		}
	};

	const updateAuthProviders = (authProviders: AuthProvider[]) => {
		update((state) => ({
			...state,
			authProviders,
			authProviderConfigured: authProviders.some((provider) => provider.configured)
		}));
	};

	const setModelProviderConfigured = (modelProviderConfigured: boolean) => {
		update((state) => ({ ...state, modelProviderConfigured }));
	};

	const updateEula = (eulaAccepted: boolean) => {
		update((state) => ({
			...state,
			eulaAccepted
		}));
	};

	return {
		subscribe,
		refresh,
		initialize,
		fetchData,
		updateAuthProviders,
		setModelProviderConfigured,
		updateEula
	};
};

export const adminConfigStore = createAdminConfigStore();
