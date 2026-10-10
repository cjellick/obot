<script lang="ts">
	import Skeleton from '$lib/components/Skeleton.svelte';
	import ProviderCard from '$lib/components/admin/ProviderCard.svelte';
	import AddProviderDialog from '$lib/components/llm-gateway/AddProviderDialog.svelte';
	import GatewayStatusBanner from '$lib/components/llm-gateway/GatewayStatusBanner.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import type { ModelProvider } from '$lib/services';
	import type { BaseProvider } from '$lib/services/admin/types';
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
	import LegacyProvidersView from './LegacyProvidersView.svelte';
	import { Import, Plus, Server } from '@lucide/svelte';
	import { onMount } from 'svelte';
	import { twMerge } from 'tailwind-merge';

	const FALLBACK_ICON = '/admin/assets/llm_gateway_icon.svg';

	interface Props {
		modelProviders: ModelProvider[];
		onFirstConfigure?: (required: boolean) => void;
		// Reports whether the gateway is running, so the page can offer actions that need it.
		onReadyChange?: (ready: boolean) => void;
	}

	let { modelProviders, onFirstConfigure, onReadyChange }: Props = $props();

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
			onReadyChange?.(!!status.ready);
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

	export function openAddProvider() {
		addDialog?.open();
	}

	function toCard(p: Provider): BaseProvider {
		const base = p.custom_provider_config?.base_provider_type;
		return {
			id: p.name,
			name: base ? p.name : providerLabel(p.name),
			icon: providerIcon(base ?? p.name) ?? FALLBACK_ICON,
			configured: true,
			created: '',
			image: '',
			port: 0
		};
	}

	function openProvider(name: string) {
		goto(`/models/providers/${encodeURIComponent(name)}`);
	}

	onMount(load);
</script>

<div class="flex flex-col gap-8">
	<GatewayStatusBanner {status} showNoProviders={false} />

	{#if loading}
		<div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
			{#each Array.from({ length: 4 }) as _, i (i)}
				<Skeleton type="card" class="h-56 w-full" />
			{/each}
		</div>
	{:else if status?.ready && providers.length === 0}
		<div class="mt-12 flex w-md max-w-full flex-col items-center gap-4 self-center text-center">
			<Server class="text-muted-content size-24 opacity-25" />
			<h4 class="text-muted-content text-lg font-semibold">No providers</h4>
			<p class="text-muted-content text-sm font-light">
				Add a provider such as OpenAI, Anthropic, Bedrock, Vertex or a self-hosted vLLM server, or
				import the legacy providers below.
			</p>
			{#if !readonly}
				<button class="btn btn-primary flex items-center gap-1 text-sm" onclick={openAddProvider}>
					<Plus class="size-4" /> Add Provider
				</button>
			{/if}
		</div>
	{:else if providers.length}
		<div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
			{#each providers as p (p.name)}
				{@const ks = keys[p.name] ?? []}
				{@const enabled = ks.filter((k) => k.enabled !== false).length}
				<ProviderCard provider={toCard(p)} {readonly} onConfigure={() => openProvider(p.name)}>
					{#snippet configuredActions()}
						<span class="text-muted-content mr-2 flex items-center gap-2 text-xs">
							{#if p.provider_status && p.provider_status !== 'active'}
								<span class="text-warning">{p.provider_status}</span>
							{/if}
							<span>
								{p.custom_provider_config?.is_key_less
									? 'No keys'
									: `${ks.length} key${ks.length === 1 ? '' : 's'}${
											enabled !== ks.length ? ` (${enabled} enabled)` : ''
										}`}
							</span>
						</span>
					{/snippet}
				</ProviderCard>
			{/each}
		</div>
	{/if}

	<section class="flex flex-col gap-4">
		<div class="flex flex-wrap items-center justify-between gap-2">
			<div>
				<h2 class="text-lg font-semibold">Legacy providers</h2>
				<p class="text-muted-content text-sm">
					Per-provider integrations Obot used before the gateway. Import them to serve their models
					through the gateway.
				</p>
			</div>
			{#if !readonly && status?.ready}
				<button
					class="btn btn-secondary flex items-center gap-1 text-sm"
					onclick={runImport}
					disabled={importing}
				>
					{#if importing}<Loading class="size-4" />{:else}<Import class="size-4" />{/if}
					Import legacy providers
				</button>
			{/if}
		</div>

		{#if importResults?.length}
			<div class="paper gap-1 text-sm">
				<h3 class="font-medium">Import results</h3>
				{#each importResults as r (r.modelProvider)}
					<div class="flex flex-wrap gap-2">
						<span class="w-64 font-mono text-xs">{r.modelProvider}</span>
						<span
							class={twMerge(
								r.status === 'imported' && 'text-success',
								r.status === 'error' && 'text-error',
								r.status === 'skipped' && 'text-muted-content'
							)}>{r.status}</span
						>
						{#if r.message}<span class="text-muted-content">{r.message}</span>{/if}
					</div>
				{/each}
			</div>
		{/if}

		<LegacyProvidersView {modelProviders} {onFirstConfigure} />
	</section>
</div>

<AddProviderDialog
	bind:this={addDialog}
	configured={providers.map((p) => p.name)}
	existingKeyNames={allKeyNames}
	onCreated={(name) => {
		success.add(`Added ${providerLabel(name)}.`);
		openProvider(name);
	}}
/>
