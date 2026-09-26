<script lang="ts">
	import Layout from '$lib/components/Layout.svelte';
	import AddProviderDialog from '$lib/components/llm-gateway/AddProviderDialog.svelte';
	import GatewayStatusBanner from '$lib/components/llm-gateway/GatewayStatusBanner.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		getStatus,
		importLegacyProviders,
		listProviderKeys,
		listProviders,
		type GatewayStatus,
		type LegacyImportResult,
		type Provider,
		type ProviderKey
	} from '$lib/services/llm-gateway/gateway';
	import { providerIcon, providerLabel } from '$lib/services/llm-gateway/providers';
	import { profile } from '$lib/stores';
	import { success } from '$lib/stores/success';
	import { goto } from '$lib/url';
	import { Import, Plus, Server } from '@lucide/svelte';
	import { onMount } from 'svelte';

	let status = $state<GatewayStatus>();
	let providers = $state<Provider[]>([]);
	let keys = $state<Record<string, ProviderKey[]>>({});
	let loading = $state(true);
	let importing = $state(false);
	let importResults = $state<LegacyImportResult[]>();
	let addDialog = $state<ReturnType<typeof AddProviderDialog>>();

	let readonly = $derived(profile.current.isAdminReadonly?.());
	let allKeyNames = $derived(Object.values(keys).flatMap((ks) => ks.map((k) => k.name)));

	async function load() {
		loading = true;
		try {
			status = await getStatus();
			if (!status.ready) return;
			providers = (await listProviders()).sort((a, b) => a.name.localeCompare(b.name));
			const entries = await Promise.all(
				providers.map(async (p) => [p.name, await listProviderKeys(p.name).catch(() => [])])
			);
			keys = Object.fromEntries(entries);
		} finally {
			loading = false;
		}
	}

	async function runImport() {
		importing = true;
		try {
			importResults = await importLegacyProviders();
			const imported = importResults.filter((r) => r.status === 'imported').length;
			success.add(
				imported
					? `Imported ${imported} provider${imported === 1 ? '' : 's'}.`
					: 'No legacy providers to import.'
			);
			await load();
		} finally {
			importing = false;
		}
	}

	onMount(load);
</script>

<svelte:head>
	<title>Obot | LLM Gateway Providers</title>
</svelte:head>

<Layout title="Providers" classes={{ childrenContainer: 'max-w-none' }}>
	{#snippet rightNavActions()}
		{#if !readonly && status?.ready}
			<div class="flex items-center gap-2">
				<button
					class="btn btn-secondary flex items-center gap-1 text-sm"
					onclick={runImport}
					disabled={importing}
					title="Copy credentials from Obot's previously configured model providers"
				>
					{#if importing}<Loading class="size-4" />{:else}<Import class="size-4" />{/if}
					Import legacy providers
				</button>
				<button
					class="btn btn-primary flex items-center gap-1 text-sm"
					onclick={() => addDialog?.open()}
				>
					<Plus class="size-4" /> Add Provider
				</button>
			</div>
		{/if}
	{/snippet}

	<div class="flex flex-col gap-6">
		<p class="text-muted-content max-w-4xl text-sm">
			Upstream providers the gateway routes to. Add several keys to a provider to load-balance
			across them by weight; failing keys are rotated out automatically. Every model of every
			provider becomes available to users through access policies.
		</p>

		<GatewayStatusBanner {status} showNoProviders={false} />

		{#if importResults?.length}
			<div class="paper flex flex-col gap-1 p-4 text-sm">
				<h4 class="font-medium">Legacy provider import</h4>
				{#each importResults as r (r.modelProvider)}
					<div class="flex gap-2">
						<span class="w-64 font-mono text-xs">{r.modelProvider}</span>
						<span
							class={r.status === 'imported'
								? 'text-success'
								: r.status === 'error'
									? 'text-error'
									: 'text-muted-content'}>{r.status}</span
						>
						{#if r.message}<span class="text-muted-content">{r.message}</span>{/if}
					</div>
				{/each}
			</div>
		{/if}

		{#if loading}
			<div class="flex justify-center p-12"><Loading class="size-8" /></div>
		{:else if status?.ready && providers.length === 0}
			<div class="mt-12 flex w-md flex-col items-center gap-4 self-center text-center">
				<Server class="text-base-content/80 size-24 opacity-25" />
				<h4 class="text-muted-content text-lg font-semibold">No providers</h4>
				<p class="text-muted-content text-sm font-light">
					Add a provider such as OpenAI, Anthropic, Bedrock, Vertex or a self-hosted vLLM server, or
					import the model providers you configured in Obot before.
				</p>
				{#if !readonly}
					<button
						class="btn btn-primary flex items-center gap-1 text-sm"
						onclick={() => addDialog?.open()}
					>
						<Plus class="size-4" /> Add Provider
					</button>
				{/if}
			</div>
		{:else}
			<div class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
				{#each providers as p (p.name)}
					{@const ks = keys[p.name] ?? []}
					{@const base = p.custom_provider_config?.base_provider_type}
					<button
						class="paper hover:border-primary flex flex-col gap-3 border border-transparent p-4 text-left transition-colors"
						onclick={() => goto(`/llm-gateway/providers/${encodeURIComponent(p.name)}`)}
					>
						<div class="flex items-center gap-3">
							{#if providerIcon(base ?? p.name)}
								<img
									src={providerIcon(base ?? p.name)}
									alt=""
									class="size-8 rounded object-contain"
								/>
							{:else}
								<div
									class="bg-base-200 dark:bg-base-300 flex size-8 items-center justify-center rounded text-xs font-bold uppercase"
								>
									{p.name.slice(0, 2)}
								</div>
							{/if}
							<div class="flex flex-col">
								<span class="font-semibold">{base ? p.name : providerLabel(p.name)}</span>
								<span class="text-muted-content text-xs">
									{base ? `Custom · ${providerLabel(base)}-compatible` : p.name}
								</span>
							</div>
							<span
								class={`ml-auto rounded-full px-2 py-0.5 text-xs ${
									p.provider_status === 'active'
										? 'bg-success/15 text-success'
										: 'bg-warning/15 text-warning'
								}`}
							>
								{p.provider_status ?? 'unknown'}
							</span>
						</div>
						<div class="text-muted-content flex flex-wrap gap-x-4 gap-y-1 text-xs">
							<span>{ks.length} key{ks.length === 1 ? '' : 's'}</span>
							<span>{ks.filter((k) => k.enabled !== false).length} enabled</span>
							<span>Retries: {p.network_config?.max_retries ?? 0}</span>
							{#if p.network_config?.base_url}
								<span class="truncate">{p.network_config.base_url}</span>
							{/if}
						</div>
					</button>
				{/each}
			</div>
		{/if}
	</div>
</Layout>

<AddProviderDialog
	bind:this={addDialog}
	configured={providers.map((p) => p.name)}
	existingKeyNames={allKeyNames}
	onCreated={(name) => {
		success.add(`Added ${providerLabel(name)}.`);
		goto(`/llm-gateway/providers/${encodeURIComponent(name)}`);
	}}
/>
