<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import Confirm from '$lib/components/Confirm.svelte';
	import Search from '$lib/components/Search.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import { UserService } from '$lib/services';
	import {
		ACTION_LABELS,
		APPLY_TO_LABELS,
		deleteGuardrailRule,
		listGuardrailProfiles,
		listGuardrailRules,
		type GuardrailProfile,
		type GuardrailRule
	} from '$lib/services/llm-gateway/guardrails';
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
	import { ShieldCheck, Trash2 } from '@lucide/svelte';
	import { onMount } from 'svelte';

	let rules = $state<GuardrailRule[]>([]);
	let detectors = $state<GuardrailProfile[]>([]);
	let userNames = $state<Record<string, string>>({});
	let loading = $state(true);
	let toDelete = $state<GuardrailRule>();

	let readonly = $derived(profile.current.isAdminReadonly?.());
	let query = $state(page.url.searchParams.get('query') || '');
	let urlFilters = $derived(getTableUrlParamsFilters());
	let initSort = $derived(getTableUrlParamsSort());

	async function load() {
		loading = true;
		try {
			const [allRules, allDetectors] = await Promise.all([
				listGuardrailRules(),
				listGuardrailProfiles()
			]);
			// MCP guardrails are configured as MCP server filters; this tab only covers models.
			rules = allRules.filter((r) => r.target === 'llm');
			detectors = allDetectors;
			if (rules.some((r) => r.subjects?.some((s) => s.type === 'user'))) {
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

	function assigned(r: GuardrailRule) {
		if (!r.subjects?.length || r.subjects.some((s) => s.type === 'everyone')) return 'Everyone';
		return r.subjects
			.map((s) => (s.type === 'user' ? (userNames[s.id ?? ''] ?? s.id) : `Group: ${s.id}`))
			.join(', ');
	}

	let detectorNames = $derived(Object.fromEntries(detectors.map((p) => [p.id, p.name])));

	let rows = $derived(
		rules.map((r) => ({
			...r,
			id: r.id ?? r.name,
			stageLabel: APPLY_TO_LABELS[r.applyTo] ?? r.applyTo,
			actionLabel: ACTION_LABELS[r.action] ?? r.action,
			detectorsLabel: (r.profileIDs ?? []).map((id) => detectorNames[id] ?? id).join(', '),
			assignedLabel: assigned(r),
			statusLabel: r.enabled ? 'Enabled' : 'Disabled'
		}))
	);

	let filteredRows = $derived(
		rows.filter((r) =>
			[r.name, r.description, r.detectorsLabel, r.assignedLabel].some((v) =>
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
{:else if rules.length === 0}
	<div class="mt-12 flex w-md max-w-full flex-col items-center gap-4 self-center text-center">
		<ShieldCheck class="text-muted-content size-24 opacity-25" />
		<h4 class="text-muted-content text-lg font-semibold">No model guardrail rules</h4>
		<p class="text-muted-content text-sm font-light">
			Rules check model requests and responses with
			<a href={resolve('/guardrails?view=detectors')} class="text-link">detectors</a>, then block,
			redact or log what they find. MCP guardrails are configured in
			<a href={resolve('/mcp-servers?view=filters')} class="text-link">MCP Servers → Filters</a>.
		</p>
	</div>
{:else}
	<div class="flex flex-col gap-2">
		<div class="bg-base-200 dark:bg-base-100 sticky top-16 left-0 z-20 w-full py-1">
			<Search
				value={query}
				class="dark:bg-base-200 dark:border-base-400 bg-base-100 border border-transparent shadow-sm"
				onChange={updateQuery}
				placeholder="Search guardrail rules..."
			/>
		</div>
		<Table
			data={filteredRows}
			fields={[
				'name',
				'stageLabel',
				'actionLabel',
				'detectorsLabel',
				'assignedLabel',
				'statusLabel'
			]}
			headers={[
				{ property: 'name', title: 'Name' },
				{ property: 'stageLabel', title: 'Stage' },
				{ property: 'actionLabel', title: 'Action' },
				{ property: 'detectorsLabel', title: 'Detectors' },
				{ property: 'assignedLabel', title: 'Assigned to' },
				{ property: 'statusLabel', title: 'Status' }
			]}
			sortable={['name', 'actionLabel', 'statusLabel']}
			filterable={['stageLabel', 'actionLabel', 'statusLabel']}
			filters={urlFilters}
			onFilter={setFilterUrlParams}
			onClearAllFilters={clearFilters}
			onSort={setSortUrlParams}
			{initSort}
			onClickRow={(row, isCtrl) => openUrl(`/models/guardrails/${row.id}`, isCtrl)}
		>
			{#snippet actions(row)}
				{#if !readonly}
					<IconButton
						variant="danger"
						tooltip={{ text: 'Delete rule' }}
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
	msg={`Delete ${toDelete?.name}? It stops applying immediately.`}
	onsuccess={async () => {
		if (toDelete?.id) await deleteGuardrailRule(toDelete.id);
		toDelete = undefined;
		await load();
	}}
	oncancel={() => (toDelete = undefined)}
/>
