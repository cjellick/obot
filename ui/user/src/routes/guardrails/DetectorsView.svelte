<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import Confirm from '$lib/components/Confirm.svelte';
	import ResponsiveDialog from '$lib/components/ResponsiveDialog.svelte';
	import Search from '$lib/components/Search.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import { parseErrorContent } from '$lib/errors';
	import {
		deleteGuardrailProfile,
		listGuardrailProfiles,
		listGuardrailProviders,
		type GuardrailProfile,
		type GuardrailProvider
	} from '$lib/services/llm-gateway/guardrails';
	import { profile } from '$lib/stores';
	import {
		clearUrlParams,
		getTableUrlParamsFilters,
		getTableUrlParamsSort,
		goto,
		replaceState,
		setFilterUrlParams,
		setSortUrlParams
	} from '$lib/url';
	import { openUrl } from '$lib/utils';
	import { CircleAlert, Info, ShieldCheck, Trash2 } from '@lucide/svelte';
	import { onMount } from 'svelte';
	import { twMerge } from 'tailwind-merge';

	let detectors = $state<GuardrailProfile[]>([]);
	let providers = $state<GuardrailProvider[]>([]);
	let loading = $state(true);
	let toDelete = $state<GuardrailProfile>();
	let deleteError = $state('');
	let typeDialog = $state<ReturnType<typeof ResponsiveDialog>>();

	let readonly = $derived(profile.current.isAdminReadonly?.());
	let query = $derived(page.url.searchParams.get('query') || '');
	let urlFilters = $derived(getTableUrlParamsFilters());
	let initSort = $derived(getTableUrlParamsSort());

	async function load() {
		loading = true;
		try {
			[detectors, providers] = await Promise.all([
				listGuardrailProfiles(),
				listGuardrailProviders()
			]);
		} finally {
			loading = false;
		}
	}

	onMount(load);

	export function openTypePicker() {
		typeDialog?.open();
	}

	let providersByType = $derived(Object.fromEntries(providers.map((p) => [p.type, p])));

	// Providers arrive sorted by category, then label.
	let categories = $derived(
		providers.reduce<{ name: string; providers: GuardrailProvider[] }[]>((acc, p) => {
			const last = acc[acc.length - 1];
			if (last?.name === p.category) last.providers.push(p);
			else acc.push({ name: p.category, providers: [p] });
			return acc;
		}, [])
	);

	let rows = $derived(
		detectors.map((p) => ({
			...p,
			id: p.id ?? p.name,
			typeLabel: providersByType[p.type]?.label ?? p.type,
			categoryLabel: providersByType[p.type]?.category ?? '',
			statusLabel: p.error ? p.error : 'Ready'
		}))
	);
	let filteredRows = $derived(
		rows.filter((r) =>
			[r.name, r.typeLabel, r.categoryLabel].some((v) =>
				v?.toLowerCase().includes(query.toLowerCase())
			)
		)
	);

	function updateQuery(value: string) {
		if (value) {
			page.url.searchParams.set('query', value);
		} else {
			page.url.searchParams.delete('query');
		}
		replaceState(page.url, { query: value });
	}

	function clearFilters() {
		clearUrlParams(Array.from(page.url.searchParams.keys()).filter((key) => key !== 'view'));
	}
</script>

{#snippet pointer()}
	Detectors are used by guardrail rules. Configure model guardrails in
	<a href={resolve('/models?view=guardrails')} class="text-link">Models → Guardrails</a>
	and MCP guardrails in
	<a href={resolve('/mcp-servers?view=filters')} class="text-link">MCP Servers → Filters</a>.
{/snippet}

{#if loading}
	<Skeleton
		type="table"
		count={5}
		classes={{ header: 'h-14 rounded-none', body: 'rounded-none' }}
	/>
{:else if detectors.length === 0}
	<div class="mt-12 flex w-md max-w-full flex-col items-center gap-4 self-center text-center">
		<ShieldCheck class="text-muted-content size-24 opacity-25" />
		<h4 class="text-muted-content text-lg font-semibold">No guardrail detectors</h4>
		<p class="text-muted-content text-sm font-light">
			Add a detector to choose and configure a check. {@render pointer()}
		</p>
	</div>
{:else}
	<div class="flex flex-col gap-2">
		<p class="text-muted-content flex items-center gap-2 text-sm">
			<Info class="size-4 shrink-0" />
			<span>{@render pointer()}</span>
		</p>

		{#if deleteError}
			<div class="notification-error flex items-center gap-2 p-3 text-sm">
				<CircleAlert class="size-4 shrink-0" />
				{deleteError}
			</div>
		{/if}

		<div class="bg-base-200 dark:bg-base-100 sticky top-16 left-0 z-20 w-full py-1">
			<Search
				value={query}
				class="dark:bg-base-200 dark:border-base-400 bg-base-100 border border-transparent shadow-sm"
				onChange={updateQuery}
				placeholder="Search detectors..."
			/>
		</div>

		<Table
			data={filteredRows}
			fields={['name', 'typeLabel', 'categoryLabel', 'statusLabel']}
			headers={[
				{ property: 'name', title: 'Name' },
				{ property: 'typeLabel', title: 'Type' },
				{ property: 'categoryLabel', title: 'Category' },
				{ property: 'statusLabel', title: 'Status' }
			]}
			sortable={['name', 'typeLabel', 'categoryLabel']}
			filterable={['typeLabel', 'categoryLabel']}
			filters={urlFilters}
			onFilter={setFilterUrlParams}
			onClearAllFilters={clearFilters}
			onSort={setSortUrlParams}
			{initSort}
			onClickRow={(row, isCtrl) => openUrl(`/guardrails/detectors/${row.id}`, isCtrl)}
		>
			{#snippet onRenderColumn(property, row)}
				{#if property === 'statusLabel'}
					<span class={twMerge('text-xs', row.error ? 'text-error' : 'text-success')}>
						{row.statusLabel}
					</span>
				{:else}
					{row[property as keyof typeof row]}
				{/if}
			{/snippet}
			{#snippet actions(row)}
				{#if !readonly}
					<IconButton
						variant="danger"
						tooltip={{ text: 'Delete detector' }}
						onclick={(e) => {
							e.stopPropagation();
							deleteError = '';
							toDelete = row;
						}}
					>
						<Trash2 class="size-4" />
					</IconButton>
				{/if}
			{/snippet}
		</Table>
	</div>
{/if}

<ResponsiveDialog bind:this={typeDialog} title="Add Detector" class="max-w-3xl">
	<div class="flex max-h-[70vh] flex-col gap-4 overflow-y-auto pt-4">
		{#each categories as c (c.name)}
			<div class="flex flex-col gap-2">
				<h3 class="text-muted-content text-xs font-semibold uppercase">{c.name}</h3>
				<div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
					{#each c.providers as p (p.type)}
						<button
							class="dark:bg-base-300 bg-base-200 hover:bg-base-300 dark:hover:bg-base-400 flex flex-col items-start gap-1 rounded-md p-3 text-left transition-colors"
							onclick={() => {
								typeDialog?.close();
								goto(`/guardrails/detectors/new?type=${encodeURIComponent(p.type)}`);
							}}
						>
							<span class="text-sm font-medium">{p.label}</span>
							<span class="text-muted-content line-clamp-3 text-xs">{p.description}</span>
						</button>
					{/each}
				</div>
			</div>
		{/each}
	</div>
</ResponsiveDialog>

<Confirm
	show={!!toDelete}
	msg={`Delete ${toDelete?.name}?`}
	onsuccess={async () => {
		try {
			if (toDelete?.id) await deleteGuardrailProfile(toDelete.id, { dontLogErrors: true });
		} catch (e) {
			deleteError = parseErrorContent(e).message;
		}
		toDelete = undefined;
		await load();
	}}
	oncancel={() => (toDelete = undefined)}
/>
