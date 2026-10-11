<script lang="ts">
	import { page } from '$app/state';
	import Layout from '$lib/components/Layout.svelte';
	import TabLayout, { type TabView } from '$lib/components/TabLayout.svelte';
	import DefaultModels from '$lib/components/admin/DefaultModels.svelte';
	import { getAdminModels, initModels } from '$lib/context/admin/models.svelte.js';
	import { profile } from '$lib/stores';
	import { goto } from '$lib/url';
	import AccessPoliciesView from './AccessPoliciesView.svelte';
	import BudgetsView from './BudgetsView.svelte';
	import GatewaySettingsView from './GatewaySettingsView.svelte';
	import GuardrailRulesView from './GuardrailRulesView.svelte';
	import ModelsView from './ModelsView.svelte';
	import ProvidersView from './ProvidersView.svelte';
	import RoutingView from './RoutingView.svelte';
	import { Plus } from '@lucide/svelte';

	let { data } = $props();
	let isAdminReadonly = $derived(profile.current.isAdminReadonly?.());
	let hasAdminAccess = $derived(profile.current.hasAdminAccess?.() ?? data.hasAdminAccess);
	let creating = $derived(
		hasAdminAccess &&
			!isAdminReadonly &&
			page.url.searchParams.get('view') === 'access-policies' &&
			page.url.searchParams.has('new')
	);

	initModels([]);
	const adminModels = getAdminModels();
	let providersView = $state<ReturnType<typeof ProvidersView>>();
	let routingView = $state<ReturnType<typeof RoutingView>>();
	let gatewayReady = $state(false);

	const titles: Record<string, string> = {
		models: 'Models',
		providers: 'Model Providers',
		routing: 'Model Routing',
		budgets: 'Budgets & Limits',
		guardrails: 'Model Guardrails',
		'access-policies': 'Model Access Policies',
		settings: 'Gateway Settings'
	};

	let views = $derived.by(() => {
		const items: TabView[] = [{ label: 'Models', value: 'models', content: models }];
		if (hasAdminAccess) {
			items.push(
				{ label: 'Providers', value: 'providers', content: providers },
				{ label: 'Routing', value: 'routing', content: routing },
				{ label: 'Budgets & Limits', value: 'budgets', content: budgets },
				{ label: 'Guardrails', value: 'guardrails', content: guardrails },
				{ label: 'Access Policies', value: 'access-policies', content: accessPolicies },
				{ label: 'Settings', value: 'settings', content: settings }
			);
		}
		return items;
	});

	let selectedView = $derived.by(() => {
		const requested = page.url.searchParams.get('view') ?? 'models';
		return views.some((v) => v.value === requested) ? requested : 'models';
	});

	function hideCreate() {
		const url = new URL(page.url);
		url.searchParams.delete('new');
		goto(url, { replaceState: true });
	}

	function showCreate() {
		goto(`${page.url.pathname}?view=access-policies&new=true`);
	}
</script>

<svelte:head>
	<title
		>Obot | {creating ? 'Create Model Access Policy' : (titles[selectedView] ?? 'Models')}</title
	>
</svelte:head>

{#if creating}
	<Layout title="Create Model Access Policy" showBackButton onBackButtonClick={hideCreate}>
		<AccessPoliciesView modelAccessPolicies={data.modelAccessPolicies} creating />
	</Layout>
{:else}
	<TabLayout
		title="Models"
		defaultView="models"
		rightNavActions={navActions}
		{views}
		classes={{ childrenContainer: 'max-w-none' }}
	/>
{/if}

{#snippet navActions(view: string)}
	{#if view === 'providers'}
		<div class="flex items-center gap-2">
			<DefaultModels availableModels={adminModels.items} readonly={isAdminReadonly} />
			{#if !isAdminReadonly && gatewayReady}
				<button
					class="btn btn-primary flex items-center gap-1 text-sm"
					onclick={() => providersView?.openAddProvider()}
				>
					<Plus class="size-4" /> Add Provider
				</button>
			{/if}
		</div>
	{:else if !isAdminReadonly && gatewayReady && view === 'routing'}
		<button
			class="btn btn-primary flex items-center gap-1 text-sm"
			onclick={() => routingView?.open()}
		>
			<Plus class="size-4" /> Add Rule
		</button>
	{:else if !isAdminReadonly && view === 'budgets'}
		<button
			class="btn btn-primary flex items-center gap-1 text-sm"
			onclick={() => goto('/models/budgets/new')}
		>
			<Plus class="size-4" /> Add Budget
		</button>
	{:else if !isAdminReadonly && view === 'guardrails'}
		<button
			class="btn btn-primary flex items-center gap-1 text-sm"
			onclick={() => goto('/models/guardrails/new')}
		>
			<Plus class="size-4" /> Add Rule
		</button>
	{:else if !isAdminReadonly && view === 'access-policies'}
		<button class="btn btn-primary flex items-center gap-1 text-sm" onclick={showCreate}>
			<Plus class="size-4" /> Add Access Policy
		</button>
	{/if}
{/snippet}

{#snippet models()}
	<ModelsView />
{/snippet}

{#snippet providers()}
	<ProvidersView bind:this={providersView} onReadyChange={(ready) => (gatewayReady = ready)} />
{/snippet}

{#snippet routing()}
	<RoutingView bind:this={routingView} onReadyChange={(ready) => (gatewayReady = ready)} />
{/snippet}

{#snippet budgets()}
	<BudgetsView />
{/snippet}

{#snippet guardrails()}
	<GuardrailRulesView />
{/snippet}

{#snippet accessPolicies()}
	<AccessPoliciesView modelAccessPolicies={data.modelAccessPolicies} />
{/snippet}

{#snippet settings()}
	<GatewaySettingsView />
{/snippet}
