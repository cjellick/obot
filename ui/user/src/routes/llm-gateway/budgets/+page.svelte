<script lang="ts">
	import Confirm from '$lib/components/Confirm.svelte';
	import Layout from '$lib/components/Layout.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import { UserService } from '$lib/services';
	import {
		deleteBudget,
		formatMetric,
		listBudgets,
		METRIC_LABELS,
		windowLabel,
		type Budget
	} from '$lib/services/llm-gateway/gateway';
	import { profile } from '$lib/stores';
	import { goto } from '$lib/url';
	import { openUrl } from '$lib/utils';
	import { Plus, Trash2, Wallet } from '@lucide/svelte';
	import { onMount } from 'svelte';

	let budgets = $state<Budget[]>([]);
	let userNames = $state<Record<string, string>>({});
	let loading = $state(true);
	let toDelete = $state<Budget>();

	let readonly = $derived(profile.current.isAdminReadonly?.());

	async function load() {
		loading = true;
		try {
			budgets = await listBudgets();
			if (budgets.some((b) => b.subjects.some((s) => s.type === 'user'))) {
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

	function assigned(b: Budget) {
		if (!b.subjects.length) return 'Unassigned';
		return b.subjects
			.map((s) =>
				s.type === 'everyone'
					? 'Everyone'
					: s.type === 'user'
						? (userNames[s.id ?? ''] ?? s.id)
						: `Group: ${s.id}`
			)
			.join(', ');
	}

	let rows = $derived(
		budgets.map((b) => ({
			...b,
			id: b.id ?? b.name,
			assignedLabel: assigned(b),
			limitsLabel: b.limits
				.map(
					(l) =>
						`${formatMetric(l.metric, l.amount)} ${METRIC_LABELS[l.metric].toLowerCase()} ${windowLabel(l)}`
				)
				.join('; '),
			scopeLabel: b.perUser ? 'Each user' : 'Shared',
			modelsLabel: b.models?.length ? `${b.models.length} models` : 'All models',
			statusLabel: b.enabled ? 'Enabled' : 'Disabled'
		}))
	);
</script>

<svelte:head>
	<title>Obot | LLM Budgets &amp; Limits</title>
</svelte:head>

<Layout title="Budgets & Limits" classes={{ childrenContainer: 'max-w-none' }}>
	{#snippet rightNavActions()}
		{#if !readonly}
			<button
				class="btn btn-primary flex items-center gap-1 text-sm"
				onclick={() => goto('/llm-gateway/budgets/new')}
			>
				<Plus class="size-4" /> Add Budget
			</button>
		{/if}
	{/snippet}

	<div class="flex flex-col gap-6">
		<p class="text-muted-content max-w-4xl text-sm">
			Budgets cap LLM gateway spend, tokens or requests over a rolling or calendar window. A budget
			either gives each assigned user their own allowance or is one pool shared by everyone
			assigned, and can be limited to specific models. Every budget that applies to a request is
			enforced, and limits are exact across all Obot servers.
		</p>

		{#if loading}
			<div class="flex justify-center p-12"><Loading class="size-8" /></div>
		{:else if budgets.length === 0}
			<div class="mt-12 flex w-md flex-col items-center gap-4 self-center text-center">
				<Wallet class="text-base-content/80 size-24 opacity-25" />
				<h4 class="text-muted-content text-lg font-semibold">No budgets</h4>
				<p class="text-muted-content text-sm font-light">
					Without a budget, gateway usage is unlimited. Create one to cap spend or usage per user,
					per group, or for everyone.
				</p>
			</div>
		{:else}
			<Table
				data={rows}
				fields={[
					'name',
					'assignedLabel',
					'scopeLabel',
					'limitsLabel',
					'modelsLabel',
					'statusLabel'
				]}
				headers={[
					{ property: 'name', title: 'Name' },
					{ property: 'assignedLabel', title: 'Assigned to' },
					{ property: 'scopeLabel', title: 'Allowance' },
					{ property: 'limitsLabel', title: 'Limits' },
					{ property: 'modelsLabel', title: 'Models' },
					{ property: 'statusLabel', title: 'Status' }
				]}
				sortable={['name', 'statusLabel']}
				filterable={['name', 'assignedLabel']}
				onClickRow={(row, isCtrl) => openUrl(`/llm-gateway/budgets/${row.id}`, isCtrl)}
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
	msg={`Delete ${toDelete?.name}? Its limits stop applying immediately.`}
	onsuccess={async () => {
		if (toDelete?.id) await deleteBudget(toDelete.id);
		toDelete = undefined;
		await load();
	}}
	oncancel={() => (toDelete = undefined)}
/>
