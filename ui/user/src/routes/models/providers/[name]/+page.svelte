<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import Confirm from '$lib/components/Confirm.svelte';
	import Layout from '$lib/components/Layout.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import ProviderKeyDialog from '$lib/components/llm-gateway/ProviderKeyDialog.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		deleteProvider,
		deleteProviderKey,
		getProvider,
		listGatewayModels,
		listProviderKeys,
		listProviders,
		refreshProviderModels,
		secretDisplay,
		syncModels,
		updateProvider,
		updateProviderKey,
		type GatewayModel,
		type Provider,
		type ProviderKey
	} from '$lib/services/llm-gateway/gateway';
	import { providerIcon, providerLabel } from '$lib/services/llm-gateway/providers';
	import { profile } from '$lib/stores';
	import { success } from '$lib/stores/success';
	import { goto } from '$lib/url';
	import { Pencil, Plus, RefreshCw, Trash2 } from '@lucide/svelte';
	import { onMount } from 'svelte';

	const PROVIDERS_URL = '/models?view=providers';

	let name = $derived(decodeURIComponent(page.params.name ?? ''));
	let provider = $state<Provider>();
	let keys = $state<ProviderKey[]>([]);
	let allKeyNames = $state<string[]>([]);
	let models = $state<GatewayModel[]>([]);
	let loading = $state(true);
	let saving = $state(false);
	let refreshing = $state(false);
	let deleting = $state(false);
	let keyToDelete = $state<ProviderKey>();
	let keyDialog = $state<ReturnType<typeof ProviderKeyDialog>>();

	// Editable network settings.
	let baseURL = $state('');
	let timeout = $state(300);
	let retries = $state(0);
	let backoffInitial = $state(0);
	let backoffMax = $state(0);
	let streamIdle = $state(0);
	let concurrency = $state(1000);
	let bufferSize = $state(5000);
	let allowPrivate = $state(false);
	let headers = $state<{ key: string; value: string }[]>([]);

	let readonly = $derived(profile.current.isAdminReadonly?.());
	let base = $derived(provider?.custom_provider_config?.base_provider_type);

	async function load() {
		loading = true;
		try {
			provider = await getProvider(name);
			const nc = provider.network_config ?? {};
			baseURL = nc.base_url ?? '';
			timeout = nc.default_request_timeout_in_seconds ?? 300;
			retries = nc.max_retries ?? 0;
			backoffInitial = nc.retry_backoff_initial ?? 0;
			backoffMax = nc.retry_backoff_max ?? 0;
			streamIdle = nc.stream_idle_timeout_in_seconds ?? 0;
			allowPrivate = !!nc.allow_private_network;
			headers = Object.entries(nc.extra_headers ?? {}).map(([key, value]) => ({ key, value }));
			concurrency = provider.concurrency_and_buffer_size?.concurrency ?? 1000;
			bufferSize = provider.concurrency_and_buffer_size?.buffer_size ?? 5000;
			await Promise.all([loadKeys(), loadModels()]);
		} finally {
			loading = false;
		}
	}

	async function loadKeys() {
		keys = await listProviderKeys(name);
		const all = await listProviders();
		const lists = await Promise.all(all.map((p) => listProviderKeys(p.name).catch(() => [])));
		allKeyNames = lists.flat().map((k) => k.name);
	}

	async function loadModels() {
		models = (await listGatewayModels({ provider: name })).filter((m) => !m.is_deprecated);
	}

	async function saveNetwork() {
		if (!provider) return;
		saving = true;
		try {
			const extra = Object.fromEntries(
				headers.filter((h) => h.key.trim()).map((h) => [h.key.trim(), h.value])
			);
			provider = await updateProvider(name, {
				network_config: {
					...provider.network_config,
					base_url: baseURL.trim() || undefined,
					default_request_timeout_in_seconds: Number(timeout),
					max_retries: Number(retries),
					retry_backoff_initial: Number(backoffInitial),
					retry_backoff_max: Number(backoffMax),
					stream_idle_timeout_in_seconds: Number(streamIdle) || undefined,
					allow_private_network: allowPrivate,
					extra_headers: Object.keys(extra).length ? extra : undefined
				},
				concurrency_and_buffer_size: {
					concurrency: Number(concurrency),
					buffer_size: Number(bufferSize)
				}
			});
			success.add('Provider settings saved.');
		} finally {
			saving = false;
		}
	}

	async function refresh() {
		refreshing = true;
		try {
			await refreshProviderModels(name);
			await loadModels();
			await syncModels();
			success.add('Models refreshed.');
		} finally {
			refreshing = false;
		}
	}

	async function toggleKey(key: ProviderKey, enabled: boolean) {
		if (!key.id) return;
		await updateProviderKey(name, key.id, { ...key, enabled });
		await loadKeys();
	}

	onMount(load);

	let keyRows = $derived(
		keys.map((k) => ({
			...k,
			id: k.id ?? k.name,
			maskedValue: secretDisplay(k.value) || '—',
			modelsLabel: (k.models ?? []).join(', ') || 'none',
			statusLabel: k.status === 'success' ? 'healthy' : (k.status ?? 'unknown')
		}))
	);
</script>

<svelte:head>
	<title>Obot | {providerLabel(name)}</title>
</svelte:head>

<Layout
	title={base ? name : providerLabel(name)}
	showBackButton
	onBackButtonClick={() => goto(PROVIDERS_URL)}
>
	{#snippet rightNavActions()}
		{#if !readonly && provider}
			<IconButton
				variant="danger2"
				tooltip={{ text: 'Delete provider' }}
				onclick={() => (deleting = true)}
			>
				<Trash2 class="size-4" />
			</IconButton>
		{/if}
	{/snippet}

	{#if loading}
		<Skeleton
			type="table"
			count={4}
			classes={{ header: 'h-14 rounded-none', body: 'rounded-none' }}
		/>
	{:else if provider}
		<div class="flex flex-col gap-6">
			<div class="paper flex-row items-center gap-3">
				{#if providerIcon(base ?? name)}
					<img src={providerIcon(base ?? name)} alt="" class="size-10 rounded object-contain" />
				{/if}
				<div class="text-muted-content flex flex-col text-sm">
					<span>
						Models are addressed as <code class="font-mono">{name}/&lt;model&gt;</code>
						{#if base}· {providerLabel(base)}-compatible API{/if}
					</span>
					<span>Status: {provider.provider_status ?? 'unknown'}</span>
				</div>
			</div>

			<section class="paper">
				<div class="flex flex-wrap items-center justify-between gap-2">
					<div>
						<h2 class="text-lg font-semibold">Keys</h2>
						<p class="text-muted-content text-sm">
							Requests are load-balanced across enabled keys by weight.
						</p>
					</div>
					{#if !readonly && !provider.custom_provider_config?.is_key_less}
						<button
							class="btn btn-primary flex items-center gap-1 text-sm"
							onclick={() => keyDialog?.open()}
						>
							<Plus class="size-4" /> Add Key
						</button>
					{/if}
				</div>
				<Table
					data={keyRows}
					fields={['name', 'maskedValue', 'modelsLabel', 'weight', 'statusLabel', 'enabled']}
					headers={[
						{ property: 'name', title: 'Name' },
						{ property: 'maskedValue', title: 'Value' },
						{ property: 'modelsLabel', title: 'Models' },
						{ property: 'weight', title: 'Weight' },
						{ property: 'statusLabel', title: 'Status' },
						{ property: 'enabled', title: 'Enabled' }
					]}
					noDataMessage={provider.custom_provider_config?.is_key_less
						? 'This provider does not use keys.'
						: 'No keys yet. Add one to start routing to this provider.'}
				>
					{#snippet onRenderColumn(property, row)}
						{#if property === 'enabled'}
							<Toggle
								label="Enabled"
								checked={row.enabled !== false}
								disabled={readonly}
								onChange={(v) => toggleKey(row, v)}
							/>
						{:else if property === 'maskedValue'}
							<span class="font-mono text-xs">{row.maskedValue}</span>
						{:else if property === 'statusLabel'}
							<span class={row.status === 'success' ? 'text-success' : 'text-warning'}
								>{row.statusLabel}</span
							>
						{:else}
							{row[property as keyof typeof row]}
						{/if}
					{/snippet}
					{#snippet actions(row)}
						{#if !readonly}
							<IconButton tooltip={{ text: 'Edit key' }} onclick={() => keyDialog?.open(row)}>
								<Pencil class="size-4" />
							</IconButton>
							<IconButton
								variant="danger"
								tooltip={{ text: 'Delete key' }}
								onclick={() => (keyToDelete = row)}
							>
								<Trash2 class="size-4" />
							</IconButton>
						{/if}
					{/snippet}
				</Table>
			</section>

			<section class="paper">
				<div>
					<h2 class="text-lg font-semibold">Network &amp; reliability</h2>
					<p class="text-muted-content text-sm">
						How the gateway connects to this provider, retries and queues requests.
					</p>
				</div>
				<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
					<div class="flex flex-col gap-1 md:col-span-2">
						<label for="p-base-url" class="text-sm font-light">Base URL</label>
						<input
							id="p-base-url"
							class="text-input-filled"
							bind:value={baseURL}
							placeholder="Provider default"
							disabled={readonly}
						/>
					</div>
					{@render num(
						'p-timeout',
						'Request timeout (seconds)',
						() => timeout,
						(v) => (timeout = v)
					)}
					{@render num(
						'p-retries',
						'Max retries',
						() => retries,
						(v) => (retries = v),
						'Retries use exponential backoff. After the last retry, the request falls back to any fallback models it names.'
					)}
					{@render num(
						'p-backoff-initial',
						'Initial retry backoff (ms)',
						() => backoffInitial,
						(v) => (backoffInitial = v)
					)}
					{@render num(
						'p-backoff-max',
						'Max retry backoff (ms)',
						() => backoffMax,
						(v) => (backoffMax = v)
					)}
					{@render num(
						'p-stream-idle',
						'Stream idle timeout (seconds)',
						() => streamIdle,
						(v) => (streamIdle = v),
						'0 uses the default of 60 seconds.'
					)}
					{@render num(
						'p-concurrency',
						'Concurrency',
						() => concurrency,
						(v) => (concurrency = v),
						'Maximum in-flight requests to this provider.'
					)}
					{@render num(
						'p-buffer',
						'Queue size',
						() => bufferSize,
						(v) => (bufferSize = v),
						'Requests waiting for a free worker.'
					)}
					<div class="flex items-center pt-6">
						<Toggle
							label="Allow private network addresses"
							labelInline
							checked={allowPrivate}
							disabled={readonly}
							onChange={(v) => (allowPrivate = v)}
						/>
					</div>
					<div class="flex flex-col gap-2 md:col-span-2">
						<span class="text-sm font-light">Extra headers</span>
						{#each headers as h, i (i)}
							<div class="flex gap-2">
								<input
									class="text-input-filled flex-1"
									placeholder="Header"
									bind:value={h.key}
									disabled={readonly}
								/>
								<input
									class="text-input-filled flex-1"
									placeholder="Value"
									bind:value={h.value}
									disabled={readonly}
								/>
								{#if !readonly}
									<IconButton
										variant="danger"
										tooltip={{ text: 'Remove' }}
										onclick={() => headers.splice(i, 1)}
									>
										<Trash2 class="size-4" />
									</IconButton>
								{/if}
							</div>
						{/each}
						{#if !readonly}
							<button
								class="btn btn-secondary flex w-fit items-center gap-1 text-xs"
								onclick={() => headers.push({ key: '', value: '' })}
							>
								<Plus class="size-3" /> Add header
							</button>
						{/if}
					</div>
				</div>
			</section>

			<section class="paper">
				<div class="flex flex-wrap items-center justify-between gap-2">
					<div>
						<h2 class="text-lg font-semibold">Models ({models.length})</h2>
						<p class="text-muted-content text-sm">
							Models appear on the <a href={resolve('/models?view=models')} class="text-link"
								>Models</a
							> page, where access policies decide who can use them.
						</p>
					</div>
					{#if !readonly}
						<button
							class="btn btn-secondary flex items-center gap-1 text-sm"
							onclick={refresh}
							disabled={refreshing}
						>
							{#if refreshing}<Loading class="size-4" />{:else}<RefreshCw class="size-4" />{/if}
							Refresh models
						</button>
					{/if}
				</div>
				<div class="flex flex-row flex-wrap gap-1.5">
					{#each models as m (m.name)}
						<span class="bg-base-200 dark:bg-base-300 rounded px-2 py-0.5 font-mono text-xs"
							>{m.name}</span
						>
					{:else}
						<span class="text-muted-content text-sm">No models listed yet.</span>
					{/each}
				</div>
			</section>
		</div>

		{#if !readonly}
			<div
				class="bg-base-200 dark:bg-base-100 sticky bottom-0 left-0 z-40 flex w-full justify-end gap-2 py-4"
			>
				<button class="btn btn-secondary text-sm" onclick={() => goto(PROVIDERS_URL)}>Cancel</button
				>
				<button class="btn btn-primary text-sm" onclick={saveNetwork} disabled={saving}>
					{#if saving}<Loading class="size-4" />{:else}Save{/if}
				</button>
			</div>
		{/if}
	{/if}
</Layout>

{#snippet num(
	id: string,
	label: string,
	get: () => number,
	set: (v: number) => void,
	help?: string
)}
	<div class="flex flex-col gap-1">
		<label for={id} class="text-sm font-light">{label}</label>
		<input
			{id}
			type="number"
			min="0"
			class="text-input-filled"
			value={get()}
			oninput={(e) => set(Number((e.target as HTMLInputElement).value))}
			disabled={readonly}
		/>
		{#if help}<span class="text-muted-content text-xs">{help}</span>{/if}
	</div>
{/snippet}

<ProviderKeyDialog
	bind:this={keyDialog}
	provider={name}
	baseProvider={base}
	existingKeyNames={allKeyNames}
	onSaved={async () => {
		success.add('Key saved.');
		await loadKeys();
		await syncModels().catch(() => {});
		setTimeout(loadModels, 1500);
	}}
/>

<Confirm
	show={!!keyToDelete}
	msg={`Delete key ${keyToDelete?.name}?`}
	onsuccess={async () => {
		if (keyToDelete?.id) await deleteProviderKey(name, keyToDelete.id);
		keyToDelete = undefined;
		await loadKeys();
	}}
	oncancel={() => (keyToDelete = undefined)}
/>

<Confirm
	show={deleting}
	msg={`Delete ${providerLabel(name)} and all of its keys? Its models will no longer be available.`}
	onsuccess={async () => {
		await deleteProvider(name);
		deleting = false;
		goto(PROVIDERS_URL);
	}}
	oncancel={() => (deleting = false)}
/>
