<script lang="ts">
	import Confirm from '$lib/components/Confirm.svelte';
	import Layout from '$lib/components/Layout.svelte';
	import ResponsiveDialog from '$lib/components/ResponsiveDialog.svelte';
	import GuardrailsTabs from '$lib/components/llm-gateway/GuardrailsTabs.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import { parseErrorContent } from '$lib/errors';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		deleteGuardrailProfile,
		listGuardrailProfiles,
		listGuardrailProviders,
		type GuardrailProfile,
		type GuardrailProvider
	} from '$lib/services/llm-gateway/guardrails';
	import { profile } from '$lib/stores';
	import { goto } from '$lib/url';
	import { openUrl } from '$lib/utils';
	import { Plus, ShieldCheck, Trash2 } from '@lucide/svelte';
	import { onMount } from 'svelte';
	import { twMerge } from 'tailwind-merge';

	let profiles = $state<GuardrailProfile[]>([]);
	let providers = $state<GuardrailProvider[]>([]);
	let loading = $state(true);
	let toDelete = $state<GuardrailProfile>();
	let deleteError = $state('');
	let typeDialog = $state<ReturnType<typeof ResponsiveDialog>>();

	let readonly = $derived(profile.current.isAdminReadonly?.());

	async function load() {
		loading = true;
		try {
			[profiles, providers] = await Promise.all([
				listGuardrailProfiles(),
				listGuardrailProviders()
			]);
		} finally {
			loading = false;
		}
	}

	onMount(load);

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
		profiles.map((p) => ({
			...p,
			id: p.id ?? p.name,
			typeLabel: providersByType[p.type]?.label ?? p.type,
			categoryLabel: providersByType[p.type]?.category ?? '',
			statusLabel: p.error ? p.error : 'Ready'
		}))
	);
</script>

<svelte:head>
	<title>Obot | Guardrail Profiles</title>
</svelte:head>

<Layout title="Guardrails" classes={{ childrenContainer: 'max-w-none' }}>
	{#snippet rightNavActions()}
		{#if !readonly}
			<button
				class="btn btn-primary flex items-center gap-1 text-sm"
				onclick={() => typeDialog?.open()}
			>
				<Plus class="size-4" /> Add Profile
			</button>
		{/if}
	{/snippet}

	<div class="flex flex-col gap-6">
		<p class="text-muted-content max-w-4xl text-sm">
			Profiles configure the checks guardrail rules run: built-in regex, secrets detection and LLM
			Judge policies, or external services such as Presidio, AWS Bedrock Guardrails or Azure AI
			Content Safety. A profile can be used by any number of rules. Former message policies are LLM
			Judge profiles.
		</p>

		<GuardrailsTabs active="profiles" />

		{#if loading}
			<div class="flex justify-center p-12"><Loading class="size-8" /></div>
		{:else if profiles.length === 0}
			<div class="mt-12 flex w-md flex-col items-center gap-4 self-center text-center">
				<ShieldCheck class="text-base-content/80 size-24 opacity-25" />
				<h4 class="text-muted-content text-lg font-semibold">No guardrail profiles</h4>
				<p class="text-muted-content text-sm font-light">
					Add a profile to choose and configure a check, then use it in a rule.
				</p>
			</div>
		{:else}
			<Table
				data={rows}
				fields={['name', 'typeLabel', 'categoryLabel', 'statusLabel']}
				headers={[
					{ property: 'name', title: 'Name' },
					{ property: 'typeLabel', title: 'Type' },
					{ property: 'categoryLabel', title: 'Category' },
					{ property: 'statusLabel', title: 'Status' }
				]}
				sortable={['name', 'typeLabel', 'categoryLabel']}
				filterable={['typeLabel', 'categoryLabel']}
				onClickRow={(row, isCtrl) => openUrl(`/llm-gateway/guardrails/profiles/${row.id}`, isCtrl)}
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
							tooltip={{ text: 'Delete profile' }}
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
		{/if}

		{#if deleteError}
			<div class="notification-error rounded-md p-3 text-sm">{deleteError}</div>
		{/if}
	</div>
</Layout>

<ResponsiveDialog bind:this={typeDialog} title="Add Profile" class="max-w-3xl">
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
								goto(`/llm-gateway/guardrails/profiles/new?type=${encodeURIComponent(p.type)}`);
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
