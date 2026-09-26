<script lang="ts">
	import Confirm from '$lib/components/Confirm.svelte';
	import Layout from '$lib/components/Layout.svelte';
	import GatewayStatusBanner from '$lib/components/llm-gateway/GatewayStatusBanner.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import { UserService } from '$lib/services';
	import {
		deleteVirtualKey,
		durationLabel,
		formatCost,
		formatNumber,
		getStatus,
		listBudgetBindings,
		listVirtualKeys,
		type BudgetBinding,
		type GatewayStatus,
		type VirtualKey
	} from '$lib/services/llm-gateway/gateway';
	import { profile } from '$lib/stores';
	import { goto } from '$lib/url';
	import { openUrl } from '$lib/utils';
	import { Plus, Trash2, Wallet } from '@lucide/svelte';
	import { onMount } from 'svelte';

	let status = $state<GatewayStatus>();
	let keys = $state<VirtualKey[]>([]);
	let bindings = $state<BudgetBinding[]>([]);
	let userNames = $state<Record<string, string>>({});
	let loading = $state(true);
	let toDelete = $state<VirtualKey>();

	let readonly = $derived(profile.current.isAdminReadonly?.());

	async function load() {
		loading = true;
		try {
			status = await getStatus();
			if (!status.ready) return;
			[keys, bindings] = await Promise.all([listVirtualKeys(), listBudgetBindings()]);
			if (bindings.some((b) => b.subjectType === 'user')) {
				const users = await UserService.listUsers().catch(() => []);
				userNames = Object.fromEntries(
					users.map((u) => [u.id, u.displayName || u.email || u.username || u.id])
				);
			}
		} finally {
			loading = false;
		}
	}

	onMount(load);

	function assigned(vk: VirtualKey) {
		const bs = bindings.filter((b) => b.virtualKeyID === vk.id);
		if (!bs.length) return 'Unassigned';
		return bs
			.map((b) =>
				b.subjectType === 'everyone'
					? 'Everyone'
					: b.subjectType === 'user'
						? (userNames[b.subjectID ?? ''] ?? b.subjectID)
						: `Group: ${b.subjectID}`
			)
			.join(', ');
	}

	function budgetLabel(vk: VirtualKey) {
		if (!vk.budgets?.length) return 'No budget';
		return vk.budgets
			.map(
				(b) =>
					`${formatCost(b.current_usage)} / ${formatCost(b.max_limit)} ${durationLabel(b.reset_duration)}`
			)
			.join('; ');
	}

	function rateLabel(vk: VirtualKey) {
		const r = vk.rate_limit;
		if (!r) return 'No limit';
		const parts = [];
		if (r.request_max_limit)
			parts.push(
				`${formatNumber(r.request_current_usage)} / ${formatNumber(r.request_max_limit)} requests ${durationLabel(r.request_reset_duration)}`
			);
		if (r.token_max_limit)
			parts.push(
				`${formatNumber(r.token_current_usage)} / ${formatNumber(r.token_max_limit)} tokens ${durationLabel(r.token_reset_duration)}`
			);
		return parts.join('; ') || 'No limit';
	}

	let rows = $derived(
		keys.map((vk) => ({
			...vk,
			assignedLabel: assigned(vk),
			budgetLabel: budgetLabel(vk),
			rateLabel: rateLabel(vk),
			providersLabel: vk.allow_all_providers
				? 'All'
				: (vk.provider_configs ?? []).map((p) => p.provider).join(', ') || 'None',
			statusLabel: vk.is_active === false ? 'Inactive' : 'Active'
		}))
	);
</script>

<svelte:head>
	<title>Obot | LLM Budgets &amp; Limits</title>
</svelte:head>

<Layout title="Budgets & Limits" classes={{ childrenContainer: 'max-w-none' }}>
	{#snippet rightNavActions()}
		{#if !readonly && status?.ready}
			<button
				class="btn btn-primary flex items-center gap-1 text-sm"
				onclick={() => goto('/llm-gateway/virtual-keys/new')}
			>
				<Plus class="size-4" /> Add Budget
			</button>
		{/if}
	{/snippet}

	<div class="flex flex-col gap-6">
		<p class="text-muted-content max-w-4xl text-sm">
			Budgets cap spend in dollars and rate limits cap requests and tokens over a rolling window.
			Each budget can also restrict which providers and models may be used. Assign a budget to
			users, groups or everyone: requests from a user are governed by their own budget first, then a
			group's, then the everyone budget. Budgets and limits add to the daily token limits and model
			access policies Obot already enforces.
		</p>

		<GatewayStatusBanner {status} showNoProviders={false} />

		{#if loading}
			<div class="flex justify-center p-12"><Loading class="size-8" /></div>
		{:else if status?.ready && keys.length === 0}
			<div class="mt-12 flex w-md flex-col items-center gap-4 self-center text-center">
				<Wallet class="text-base-content/80 size-24 opacity-25" />
				<h4 class="text-muted-content text-lg font-semibold">No budgets</h4>
				<p class="text-muted-content text-sm font-light">
					Create a budget to cap LLM spend and request rates for a team, a user, or everyone.
				</p>
			</div>
		{:else}
			<Table
				data={rows}
				fields={[
					'name',
					'assignedLabel',
					'budgetLabel',
					'rateLabel',
					'providersLabel',
					'statusLabel'
				]}
				headers={[
					{ property: 'name', title: 'Name' },
					{ property: 'assignedLabel', title: 'Assigned to' },
					{ property: 'budgetLabel', title: 'Budget' },
					{ property: 'rateLabel', title: 'Rate limit' },
					{ property: 'providersLabel', title: 'Providers' },
					{ property: 'statusLabel', title: 'Status' }
				]}
				sortable={['name', 'statusLabel']}
				filterable={['name', 'assignedLabel']}
				onClickRow={(row, isCtrl) => openUrl(`/llm-gateway/virtual-keys/${row.id}`, isCtrl)}
			>
				{#snippet actions(row)}
					{#if !readonly}
						<IconButton
							variant="danger"
							tooltip={{ text: 'Delete budget' }}
							onclick={(e) => {
								e.stopPropagation();
								toDelete = row;
							}}
						>
							<Trash2 class="size-4" />
						</IconButton>
					{/if}
				{/snippet}
			</Table>
		{/if}
	</div>
</Layout>

<Confirm
	show={!!toDelete}
	msg={`Delete ${toDelete?.name}? Users assigned to it will no longer be limited by it.`}
	onsuccess={async () => {
		if (toDelete) await deleteVirtualKey(toDelete.id);
		toDelete = undefined;
		await load();
	}}
	oncancel={() => (toDelete = undefined)}
/>
