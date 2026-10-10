<script lang="ts">
	import { page } from '$app/state';
	import Confirm from '$lib/components/Confirm.svelte';
	import Search from '$lib/components/Search.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
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
	import {
		clearUrlParams,
		getTableUrlParamsFilters,
		getTableUrlParamsSort,
		replaceState,
		setFilterUrlParams,
		setSortUrlParams
	} from '$lib/url';
	import { openUrl } from '$lib/utils';
	import { Trash2, Wallet } from '@lucide/svelte';
	import { onMount } from 'svelte';

	let budgets = $state<Budget[]>([]);
	let userNames = $state<Record<string, string>>({});
	let loading = $state(true);
	let toDelete = $state<Budget>();

	let readonly = $derived(profile.current.isAdminReadonly?.());
	let query = $state(page.url.searchParams.get('query') || '');
	let urlFilters = $derived(getTableUrlParamsFilters());
	let initSort = $derived(getTableUrlParamsSort());

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

	let filteredRows = $derived(
		rows.filter((r) =>
			[r.name, r.description, r.assignedLabel].some((v) =>
				v?.toLowerCase().includes(query.toLowerCase())
			)
		)
	);

	function updateQuery(value: string) {
		query = value;
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

{#if loading}
	<Skeleton
		type="table"
		count={5}
		classes={{ header: 'h-14 rounded-none', body: 'rounded-none' }}
	/>
{:else if budgets.length === 0}
	<div class="mt-12 flex w-md max-w-full flex-col items-center gap-4 self-center text-center">
		<Wallet class="text-muted-content size-24 opacity-25" />
		<h4 class="text-muted-content text-lg font-semibold">No budgets</h4>
		<p class="text-muted-content text-sm font-light">
			Without a budget, gateway usage is unlimited. Create one to cap spend, tokens or requests per
			user, per group, or for everyone.
		</p>
	</div>
{:else}
	<div class="flex flex-col gap-2">
		<div class="bg-base-200 dark:bg-base-100 sticky top-16 left-0 z-20 w-full py-1">
			<Search
				value={query}
				class="dark:bg-base-200 dark:border-base-400 bg-base-100 border border-transparent shadow-sm"
				onChange={updateQuery}
				placeholder="Search budgets..."
			/>
		</div>
		<Table
			data={filteredRows}
			fields={['name', 'assignedLabel', 'scopeLabel', 'limitsLabel', 'modelsLabel', 'statusLabel']}
			headers={[
				{ property: 'name', title: 'Name' },
				{ property: 'assignedLabel', title: 'Assigned to' },
				{ property: 'scopeLabel', title: 'Allowance' },
				{ property: 'limitsLabel', title: 'Limits' },
				{ property: 'modelsLabel', title: 'Models' },
				{ property: 'statusLabel', title: 'Status' }
			]}
			sortable={['name', 'scopeLabel', 'statusLabel']}
			filterable={['scopeLabel', 'statusLabel']}
			filters={urlFilters}
			onFilter={setFilterUrlParams}
			onClearAllFilters={clearFilters}
			onSort={setSortUrlParams}
			{initSort}
			onClickRow={(row, isCtrl) => openUrl(`/models/budgets/${row.id}`, isCtrl)}
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
	</div>
{/if}

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
