<script lang="ts">
	import { page } from '$app/state';
	import { buildPillSearchParamFilters, buildSearchParamFiltersArray } from '$lib/auditlogs';
	import FilterPills from '$lib/components/FilterPills.svelte';
	import Search from '$lib/components/Search.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import AuditLogCalendar from '$lib/components/admin/audit-logs/AuditLogCalendar.svelte';
	import FiltersDrawer from '$lib/components/admin/filters-drawer/FiltersDrawer.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import { isAbortError } from '$lib/errors';
	import { AdminService, UserService, type OrgUser } from '$lib/services';
	import {
		APPLY_TO_LABELS,
		findingsSummary,
		listGuardrailEvents,
		listGuardrailRules,
		OUTCOME_LABELS,
		TARGET_LABELS,
		type GuardrailEvent,
		type GuardrailOutcome,
		type GuardrailRule,
		type GuardrailTarget
	} from '$lib/services/llm-gateway/guardrails';
	import { goto, replaceState } from '$lib/url';
	import { getUserDisplayName } from '$lib/utils';
	import { ChevronLeft, ChevronRight, CircleAlert, Funnel, ShieldCheck } from '@lucide/svelte';
	import { endOfDay, set, subDays } from 'date-fns';
	import { debounce } from 'es-toolkit';
	import { onMount } from 'svelte';
	import { twMerge } from 'tailwind-merge';

	type EventURLFilters = {
		outcome?: string | null;
		target?: string | null;
		rule_id?: string | null;
	};
	type FilterKey = keyof EventURLFilters;

	const supportedFilters: FilterKey[] = ['outcome', 'target', 'rule_id'];
	const pageLimit = 500;

	let loading = $state(true);
	let fetchError = $state<string | null>(null);
	let events = $state<GuardrailEvent[]>([]);
	let total = $state(0);
	let pageIndex = $state(0);
	let users = $state<OrgUser[]>([]);
	// Events come from model rules and from MCP guardrail filters (whose rule ID is the
	// filter's ID), so both are offered in the Rule filter.
	let rules = $state<{ id: string; name: string }[]>([]);
	let showFilters = $state(false);
	let rightSidebar = $state<HTMLDivElement>();

	let usersMap = $derived(new Map(users.map((u) => [u.id, u])));
	let ruleNames = $derived(new Map(rules.map((r) => [r.id, r.name])));
	let query = $derived(page.url.searchParams.get('query') ?? '');

	const numberOfPages = $derived(Math.ceil(total / pageLimit));
	const isReachedMin = $derived(pageIndex <= 0);
	const isReachedMax = $derived(pageIndex >= numberOfPages - 1);

	const searchParamFiltersAsArray = $derived(
		buildSearchParamFiltersArray<EventURLFilters>(supportedFilters)
	);
	const searchParamFilters = $derived(
		Object.fromEntries(searchParamFiltersAsArray) as Record<FilterKey, string | null | undefined>
	);
	const pillsSearchParamFilters = $derived(
		buildPillSearchParamFilters<EventURLFilters>(searchParamFiltersAsArray)
	);

	const timeRange = $derived.by(() => {
		const startParam = page.url.searchParams.get('start_time');
		const endParam = page.url.searchParams.get('end_time');
		const endTime = set(new Date(endParam || new Date()), { milliseconds: 0, seconds: 59 });
		const startTime = startParam
			? set(new Date(startParam), { milliseconds: 0, seconds: 0 })
			: set(subDays(endTime, 7), { milliseconds: 0, seconds: 0 });
		return { startTime, endTime };
	});

	const filterKey = $derived(
		JSON.stringify({
			...pillsSearchParamFilters,
			start: timeRange.startTime.toISOString(),
			end: timeRange.endTime.toISOString()
		})
	);

	$effect(() => {
		void filterKey;
		pageIndex = 0;
	});

	$effect(() => {
		const controller = new AbortController();
		const filters = {
			limit: pageLimit,
			offset: pageIndex * pageLimit,
			outcome: (pillsSearchParamFilters.outcome ?? '') as GuardrailOutcome | '',
			target: (pillsSearchParamFilters.target ?? '') as GuardrailTarget | '',
			ruleID: pillsSearchParamFilters.rule_id ?? undefined,
			since: timeRange.startTime.toISOString(),
			until: timeRange.endTime.toISOString()
		};

		loading = true;
		fetchError = null;
		listGuardrailEvents(filters, { signal: controller.signal })
			.then((resp) => {
				if (controller.signal.aborted) return;
				events = resp.items;
				total = resp.total;
			})
			.catch((err) => {
				if (isAbortError(err) || controller.signal.aborted) return;
				fetchError = err instanceof Error ? err.message : 'Failed to load guardrail events';
			})
			.finally(() => {
				if (!controller.signal.aborted) loading = false;
			});

		return () => controller.abort();
	});

	onMount(() => {
		UserService.listUsersIncludeDeleted()
			.then((u) => (users = u))
			.catch(() => {});
		loadRuleSources()
			.then((r) => (rules = r))
			.catch(() => {});
	});

	async function loadRuleSources(): Promise<{ id: string; name: string }[]> {
		const [modelRules, filters] = await Promise.all([
			listGuardrailRules().catch((): GuardrailRule[] => []),
			AdminService.listMCPFilters().catch(() => [])
		]);
		return [
			...modelRules.filter((r) => r.id).map((r) => ({ id: r.id as string, name: r.name })),
			...filters.filter((f) => f.guardrail).map((f) => ({ id: f.id, name: f.name || f.id }))
		];
	}

	function userLabel(id?: string) {
		return id ? getUserDisplayName(usersMap, id) : '—';
	}

	let rows = $derived(
		events.map((ev) => ({
			...ev,
			timeLabel: new Date(ev.created).toLocaleString(),
			ruleLabel: ev.ruleName || ev.ruleID,
			detectorLabel: ev.profileName || '—',
			targetLabel: `${TARGET_LABELS[ev.target] ?? ev.target} · ${APPLY_TO_LABELS[ev.stage] ?? ev.stage}`,
			outcomeLabel: OUTCOME_LABELS[ev.outcome] ?? ev.outcome,
			userLabel: userLabel(ev.userID),
			resourceLabel: ev.model || ev.resource || '—',
			findingsLabel: ev.outcome === 'error' ? (ev.error ?? '') : findingsSummary(ev.findings) || '—'
		}))
	);

	let filteredRows = $derived.by(() => {
		const q = query.trim().toLowerCase();
		if (!q) return rows;
		return rows.filter((r) =>
			[r.ruleLabel, r.detectorLabel, r.userLabel, r.resourceLabel].some((v) =>
				v?.toLowerCase().includes(q)
			)
		);
	});

	const handleQueryChange = debounce((value: string) => {
		if (value) {
			page.url.searchParams.set('query', value);
		} else {
			page.url.searchParams.delete('query');
		}
		replaceState(page.url, {});
	}, 100);

	function handleDateChange({ start, end }: { start?: Date; end?: Date }) {
		const url = page.url;
		if (start) {
			url.searchParams.set('start_time', start.toISOString());
			url.searchParams.set('end_time', (end ?? endOfDay(start)).toISOString());
		}
		goto(url, { noScroll: true });
	}

	function getFilterDisplayLabel(key: string) {
		if (key === 'outcome') return 'Outcome';
		if (key === 'target') return 'Target';
		if (key === 'rule_id') return 'Rule';
		return key;
	}

	function getFilterValue(key: string, value: string | number) {
		const v = String(value);
		if (key === 'outcome') return OUTCOME_LABELS[v as GuardrailOutcome] ?? v;
		if (key === 'target') return TARGET_LABELS[v as GuardrailTarget] ?? v;
		if (key === 'rule_id') return ruleNames.get(v) ?? v;
		return v;
	}

	async function filterOptions(filterId: string) {
		if (filterId === 'outcome') return { options: Object.keys(OUTCOME_LABELS) };
		if (filterId === 'target') return { options: Object.keys(TARGET_LABELS) };
		if (filterId === 'rule_id') {
			rules = await loadRuleSources().catch(() => rules);
			return { options: rules.map((r) => r.id) };
		}
		return { options: [] };
	}

	function outcomeClass(o: GuardrailOutcome) {
		switch (o) {
			case 'blocked':
			case 'error':
				return 'bg-error/15 text-error';
			case 'redacted':
				return 'bg-warning/15 text-warning';
			default:
				return 'bg-base-300 text-base-content';
		}
	}

	function closeFilters() {
		rightSidebar?.hidePopover();
		showFilters = false;
	}
</script>

<div class="flex flex-col gap-2 @container">
	<div class="flex flex-col gap-4 @min-[768px]:flex-row">
		<Search
			class="dark:bg-base-200 dark:border-base-400 bg-base-100 border border-transparent shadow-sm"
			onChange={handleQueryChange}
			placeholder="Search events..."
			value={query}
		/>
		<div class="flex gap-4 self-start @min-[768px]:self-end">
			<AuditLogCalendar
				start={timeRange.startTime}
				end={timeRange.endTime}
				onChange={handleDateChange}
			/>
			<button
				class="btn btn-neutral h-12.5"
				onclick={() => {
					showFilters = true;
					rightSidebar?.showPopover();
				}}
			>
				<Funnel class="size-4" />
				Filters
			</button>
		</div>
	</div>

	<FilterPills {pillsSearchParamFilters} {getFilterDisplayLabel} {getFilterValue} />
</div>

{#if loading}
	<Skeleton
		type="table"
		count={8}
		classes={{ header: 'h-14 rounded-none', body: 'rounded-none' }}
	/>
{:else if fetchError}
	<div class="notification-error flex w-full items-center gap-3 p-4">
		<CircleAlert class="size-5 shrink-0" />
		<div class="flex flex-col gap-1">
			<p class="text-sm font-semibold">Unable to load guardrail events</p>
			<p class="text-sm font-light">{fetchError}</p>
		</div>
	</div>
{:else if rows.length === 0}
	<div class="mt-12 flex w-md max-w-full flex-col items-center gap-4 self-center text-center">
		<ShieldCheck class="text-muted-content size-24 opacity-25" />
		<h4 class="text-muted-content text-lg font-semibold">No guardrail events</h4>
		<p class="text-muted-content text-sm font-light">
			No guardrails blocked, redacted or logged anything in this range.
		</p>
	</div>
{:else}
	<Table
		data={filteredRows}
		fields={[
			'timeLabel',
			'ruleLabel',
			'detectorLabel',
			'targetLabel',
			'outcomeLabel',
			'userLabel',
			'resourceLabel',
			'findingsLabel'
		]}
		headers={[
			{ property: 'timeLabel', title: 'Time' },
			{ property: 'ruleLabel', title: 'Rule' },
			{ property: 'detectorLabel', title: 'Detector' },
			{ property: 'targetLabel', title: 'Target / Stage' },
			{ property: 'outcomeLabel', title: 'Outcome' },
			{ property: 'userLabel', title: 'User' },
			{ property: 'resourceLabel', title: 'Model / Resource' },
			{ property: 'findingsLabel', title: 'Findings' }
		]}
		noDataMessage="No events match the search."
		pageSize={50}
	>
		{#snippet onRenderColumn(property, row)}
			{#if property === 'outcomeLabel'}
				<span class={twMerge('rounded-full px-2 py-0.5 text-xs', outcomeClass(row.outcome))}>
					{row.outcomeLabel}
				</span>
			{:else if property === 'findingsLabel' && row.outcome === 'error'}
				<span class="text-error text-xs">{row.findingsLabel}</span>
			{:else}
				{row[property as keyof typeof row]}
			{/if}
		{/snippet}
	</Table>
{/if}

{#if !loading && numberOfPages > 1}
	<div class="bg-base-200 dark:bg-base-100 sticky bottom-0 left-0 z-50 w-full py-4">
		<div class="text-muted-content flex items-center justify-between gap-4 px-1 text-sm">
			<button
				class="hover:text-base-content flex items-center gap-1 disabled:opacity-50"
				disabled={isReachedMin}
				onclick={() => (pageIndex -= 1)}
			>
				<ChevronLeft class="size-4" /> Previous Page
			</button>
			<div>
				{Intl.NumberFormat().format(pageIndex + 1)} of {Intl.NumberFormat().format(numberOfPages)} pages
			</div>
			<button
				class="hover:text-base-content flex items-center gap-1 disabled:opacity-50"
				disabled={isReachedMax}
				onclick={() => (pageIndex += 1)}
			>
				Next Page <ChevronRight class="size-4" />
			</button>
		</div>
	</div>
{/if}

<div bind:this={rightSidebar} popover class="drawer-legacy md:w-lg lg:w-xl">
	{#if showFilters}
		<FiltersDrawer
			onClose={closeFilters}
			filters={searchParamFilters}
			getVisibleFilterKeys={() => supportedFilters}
			isFilterMultiSelect={() => false}
			getUserDisplayName={(id) => getUserDisplayName(usersMap, id)}
			{getFilterDisplayLabel}
			getFilterOptionLabel={(key, value) => getFilterValue(key, value)}
			endpoint={filterOptions}
		/>
	{/if}
</div>
