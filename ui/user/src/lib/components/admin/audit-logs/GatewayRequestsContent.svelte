<script lang="ts">
	import { page } from '$app/state';
	import { columnResize } from '$lib/actions/resize';
	import { buildPillSearchParamFilters, buildSearchParamFiltersArray } from '$lib/auditlogs';
	import FilterPills from '$lib/components/FilterPills.svelte';
	import Search from '$lib/components/Search.svelte';
	import AuditLogCalendar from '$lib/components/admin/audit-logs/AuditLogCalendar.svelte';
	import GatewayStatusBanner from '$lib/components/llm-gateway/GatewayStatusBanner.svelte';
	import { setVirtualPageData } from '$lib/components/ui/virtual-page/context';
	import { isAbortError } from '$lib/errors';
	import {
		getLogFilterData,
		getStatus,
		INFERENCE_OBJECTS,
		listLogs,
		listProviders,
		type GatewayLog,
		type GatewayStatus,
		type LogFilters
	} from '$lib/services/llm-gateway/gateway';
	import { providerLabel } from '$lib/services/llm-gateway/providers';
	import { responsive } from '$lib/stores';
	import { goto, replaceState } from '$lib/url';
	import FiltersDrawer from '../filters-drawer/FiltersDrawer.svelte';
	import AuditLogTableSkeleton from './AuditLogTableSkeleton.svelte';
	import GatewayRequestDetails from './GatewayRequestDetails.svelte';
	import GatewayRequestsTable from './GatewayRequestsTable.svelte';
	import { Captions, ChevronLeft, ChevronRight, CircleAlert, Funnel } from '@lucide/svelte';
	import { endOfDay, set, subDays } from 'date-fns';
	import { debounce } from 'es-toolkit';
	import { onMount } from 'svelte';
	import { twMerge } from 'tailwind-merge';

	type GatewayRequestURLFilters = {
		provider?: string | null;
		model?: string | null;
		status?: string | null;
		user?: string | null;
		hide_models_requests?: string | null;
		start_time?: string | null;
		end_time?: string | null;
		query?: string | null;
	};
	type SupportedFilter = Exclude<
		keyof GatewayRequestURLFilters,
		'start_time' | 'end_time' | 'query'
	>;

	const supportedFilters: SupportedFilter[] = [
		'provider',
		'model',
		'status',
		'user',
		'hide_models_requests'
	];
	const singleSelectFilters = new Set<string>(['user', 'hide_models_requests']);
	const statusOptions = ['success', 'error', 'processing'];
	const pageLimit = 500;

	let status = $state<GatewayStatus>();
	let loading = $state(true);
	let fetchError = $state<string | null>(null);
	let logs = $state<GatewayLog[]>([]);
	let total = $state(0);
	let pageIndex = $state(0);
	let showFilters = $state(false);
	let rightSidebar = $state<HTMLDivElement>();
	let selectedLog = $state<GatewayLog>();

	const numberOfPages = $derived(Math.ceil(total / pageLimit));
	const isReachedMin = $derived(pageIndex <= 0);
	const isReachedMax = $derived(pageIndex >= numberOfPages - 1);
	const pageOffset = $derived(pageIndex * pageLimit);

	let query = $derived(page.url.searchParams.get('query') ?? '');

	$effect(() => {
		setVirtualPageData(logs);
	});

	const searchParamFiltersAsArray = $derived(
		buildSearchParamFiltersArray<GatewayRequestURLFilters>(supportedFilters, {
			hide_models_requests: 'true'
		})
	);
	const searchParamFilters = $derived.by<GatewayRequestURLFilters>(() =>
		searchParamFiltersAsArray.reduce(
			(acc, [key, value]) => {
				acc[key!] = value;
				return acc;
			},
			{} as Record<string, unknown>
		)
	);
	const pillsSearchParamFilters = $derived(
		buildPillSearchParamFilters<GatewayRequestURLFilters>(searchParamFiltersAsArray)
	);
	const hasFilterPills = $derived(Object.keys(pillsSearchParamFilters).length > 0);

	const timeRangeFilters = $derived.by(() => {
		const startParam = page.url.searchParams.get('start_time');
		const endParam = page.url.searchParams.get('end_time');
		const endTime = set(new Date(endParam || new Date()), { milliseconds: 0, seconds: 59 });
		const startTime = startParam
			? set(new Date(startParam), { milliseconds: 0, seconds: 0 })
			: set(subDays(endTime, 7), { milliseconds: 0, seconds: 0 });
		return { startTime, endTime };
	});

	function splitValues(value: string | null | undefined) {
		return (value ?? '')
			.split(',')
			.map((v) => v.trim())
			.filter(Boolean);
	}

	const logFilters = $derived.by<LogFilters>(() => {
		const f: LogFilters = {
			start_time: timeRangeFilters.startTime.toISOString(),
			end_time: timeRangeFilters.endTime.toISOString()
		};
		const providers = splitValues(pillsSearchParamFilters.provider);
		const models = splitValues(pillsSearchParamFilters.model);
		const statuses = splitValues(pillsSearchParamFilters.status);
		if (providers.length) f.providers = providers;
		if (models.length) f.models = models;
		if (statuses.length) f.status = statuses;
		if (pillsSearchParamFilters.user)
			f.metadata = { 'obot-user-name': pillsSearchParamFilters.user };
		if (pillsSearchParamFilters.hide_models_requests === 'true') f.objects = INFERENCE_OBJECTS;
		if (query.trim()) f.content_search = query.trim();
		return f;
	});

	const filterPaginationKey = $derived(JSON.stringify(logFilters));

	onMount(async () => {
		try {
			status = await getStatus();
		} catch (err) {
			fetchError = err instanceof Error ? err.message : 'Failed to load gateway status';
			loading = false;
		}
	});

	$effect(() => {
		void filterPaginationKey;
		pageIndex = 0;
	});

	$effect(() => {
		if (!status) return;
		if (!status.ready) {
			loading = false;
			return;
		}

		const controller = new AbortController();
		const currentFilters = logFilters;
		const offset = pageOffset;

		loading = true;
		fetchError = null;

		listLogs(currentFilters, { limit: pageLimit, offset }, { signal: controller.signal })
			.then((res) => {
				if (controller.signal.aborted) return;
				logs = res.logs;
				total = res.total;
				if (offset > res.total) pageIndex = 0;
			})
			.catch((err) => {
				if (isAbortError(err) || controller.signal.aborted) return;
				console.error('Failed to fetch gateway requests:', err);
				fetchError = err instanceof Error ? err.message : 'Failed to load gateway requests';
			})
			.finally(() => {
				if (controller.signal.aborted) return;
				loading = false;
			});

		return () => controller.abort();
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

	function nextPage() {
		if (!isReachedMax) pageIndex += 1;
	}

	function prevPage() {
		if (!isReachedMin) pageIndex -= 1;
	}

	function getFilterDisplayLabel(key: string) {
		switch (key) {
			case 'provider':
				return 'Provider';
			case 'model':
				return 'Model';
			case 'status':
				return 'Status';
			case 'user':
				return 'User';
			case 'hide_models_requests':
				return 'Model discovery requests';
			default:
				return key.replace(/_(\w)/g, ' $1');
		}
	}

	function getFilterOptionLabel(key: string, value: string) {
		if (key === 'provider') return providerLabel(value);
		if (key === 'hide_models_requests') return value === 'true' ? 'Hidden' : 'Shown';
		return value;
	}

	function userName(log: GatewayLog) {
		return log.metadata?.['obot-user-name'] ?? log.metadata?.['obot-user-id'] ?? '';
	}

	function handleRightSidebarClose() {
		rightSidebar?.hidePopover();
		selectedLog = undefined;
		showFilters = false;
	}

	let filterData: ReturnType<typeof getLogFilterData> | undefined;
	function loadFilterData() {
		filterData ??= getLogFilterData();
		return filterData;
	}

	async function filterOptions(filterId: string) {
		switch (filterId) {
			case 'provider':
				return (await listProviders()).map((p) => p.name);
			case 'model':
				return (await loadFilterData()).models ?? [];
			case 'user':
				return (await loadFilterData()).metadata_keys?.['obot-user-name'] ?? [];
			case 'status':
				return statusOptions;
			case 'hide_models_requests':
				return ['true', 'false'];
			default:
				return [];
		}
	}
</script>

<GatewayStatusBanner {status} />

{#if !status || status.ready}
	<div class="flex flex-col gap-2 @container">
		<div class="flex flex-col gap-4 @min-[768px]:flex-row">
			<Search
				class="dark:bg-base-200 dark:border-base-400 bg-base-100 border border-transparent shadow-sm"
				onChange={handleQueryChange}
				placeholder="Search request and response content..."
				value={query}
			/>
			<div class="self-start @min-[768px]:self-end flex gap-4">
				<AuditLogCalendar
					start={timeRangeFilters.startTime}
					end={timeRangeFilters.endTime}
					onChange={handleDateChange}
				/>
				<button
					class="btn btn-neutral h-12.5"
					onclick={() => {
						showFilters = true;
						selectedLog = undefined;
						rightSidebar?.showPopover();
					}}
				>
					<Funnel class="size-4" />
					Filters
				</button>
			</div>
		</div>

		{#if hasFilterPills}
			<FilterPills
				{pillsSearchParamFilters}
				getFilterDisplayLabel={(key) => getFilterDisplayLabel(String(key))}
				getFilterValue={(key, value) => getFilterOptionLabel(String(key), String(value))}
			/>
		{/if}
	</div>

	{#if loading}
		<AuditLogTableSkeleton />
	{:else if fetchError}
		<div class="notification-error flex w-full items-center gap-3 p-4">
			<CircleAlert class="size-5 shrink-0" />
			<div class="flex flex-col gap-1">
				<p class="text-sm font-semibold">Unable to load gateway requests</p>
				<p class="text-sm font-light">{fetchError}</p>
			</div>
		</div>
	{:else if logs.length > 0}
		<GatewayRequestsTable
			onSelectRow={(log: GatewayLog) => {
				showFilters = false;
				selectedLog = log;
				rightSidebar?.showPopover();
			}}
			getUserName={userName}
		/>
	{:else}
		<div class="flex flex-col items-center justify-center gap-4 px-6 py-16 text-center w-full">
			<Captions class="text-muted-content size-20 opacity-50" />
			<h4 class="text-muted-content text-lg font-semibold">No gateway requests</h4>
			<p class="text-muted-content max-w-md text-sm font-light">
				There are no gateway requests for the selected range or search criteria.
			</p>
		</div>
	{/if}

	<div class="flex grow"></div>

	{#if !loading && total > 0 && numberOfPages > 1}
		<div class="sticky left-0 w-full bottom-0 bg-base-200 dark:bg-base-100 z-50 py-4">
			<div class="text-muted-content flex items-center justify-between gap-4 px-1 text-sm">
				<button
					class="hover:text-base-content flex gap-1 items-center disabled:opacity-50"
					disabled={isReachedMin}
					onclick={prevPage}
				>
					<ChevronLeft class="size-4" /> Previous Page
				</button>
				<div>
					{Intl.NumberFormat().format(pageIndex + 1)} of {Intl.NumberFormat().format(
						numberOfPages || 1
					)} pages
				</div>
				<button
					class="hover:text-base-content flex gap-1 items-center disabled:opacity-50"
					disabled={isReachedMax}
					onclick={nextPage}
				>
					Next Page <ChevronRight class="size-4" />
				</button>
			</div>
		</div>
	{/if}
{/if}

<div
	bind:this={rightSidebar}
	popover
	class={twMerge(
		'drawer-legacy',
		selectedLog ? 'md:max-w-[85vw] md:min-w-lg min-w-full max-w-full' : 'md:w-lg lg:w-xl'
	)}
	style={selectedLog && !responsive.isMobile ? 'width: 32rem' : ''}
>
	{#if selectedLog && !responsive.isMobile && rightSidebar}
		<div
			role="none"
			class="absolute top-0 left-0 z-30 h-full w-3 cursor-col-resize"
			use:columnResize={{ column: rightSidebar, direction: 'right' }}
		></div>
	{/if}
	{#if selectedLog}
		<GatewayRequestDetails log={selectedLog} onClose={handleRightSidebarClose} />
	{:else if showFilters}
		<FiltersDrawer
			onClose={handleRightSidebarClose}
			filters={searchParamFilters}
			getVisibleFilterKeys={() => supportedFilters}
			isFilterMultiSelect={(filterId) => !singleSelectFilters.has(filterId)}
			getDefaultValue={(filterId) => (filterId === 'hide_models_requests' ? 'true' : undefined)}
			getUserDisplayName={(id) => id}
			{getFilterDisplayLabel}
			{getFilterOptionLabel}
			endpoint={async (filterId) => ({ options: await filterOptions(filterId) })}
		/>
	{/if}
</div>
