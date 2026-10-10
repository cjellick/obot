<script lang="ts">
	import { page } from '$app/state';
	import type { DateRange } from '$lib/components/Calendar.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import TweenedMetric from '$lib/components/TweenedMetric.svelte';
	import AuditLogCalendar from '$lib/components/admin/audit-logs/AuditLogCalendar.svelte';
	import StackedTimeline from '$lib/components/graph/StackedTimeline.svelte';
	import GatewayStatusBanner from '$lib/components/llm-gateway/GatewayStatusBanner.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import { PAGE_TRANSITION_DURATION } from '$lib/constants';
	import {
		formatCost,
		formatNumber,
		getHistogram,
		getRankings,
		getStats,
		getStatus,
		INFERENCE_OBJECTS,
		type GatewayStatus,
		type Histogram,
		type LogFilters,
		type LogStats,
		type ModelRanking
	} from '$lib/services/llm-gateway/gateway';
	import { providerLabel } from '$lib/services/llm-gateway/providers';
	import { goto } from '$lib/url';
	import { subDays } from 'date-fns';
	import { fade } from 'svelte/transition';

	const START_PARAM = 'start_time';
	const END_PARAM = 'end_time';

	let status = $state<GatewayStatus>();
	let stats = $state<LogStats>();
	let requests = $state<Histogram>();
	let costByProvider = $state<Histogram>();
	let rankings = $state<ModelRanking[]>([]);
	let loading = $state(true);

	let endParam = $derived(page.url.searchParams.get(END_PARAM));
	let startParam = $derived(page.url.searchParams.get(START_PARAM));
	let endDate = $derived(endParam ? new Date(endParam) : new Date());
	let startDate = $derived(startParam ? new Date(startParam) : subDays(endDate, 7));

	$effect(() => {
		const filters: LogFilters = {
			start_time: startDate.toISOString(),
			end_time: endDate.toISOString(),
			objects: INFERENCE_OBJECTS
		};
		let cancelled = false;
		loading = true;
		(async () => {
			try {
				const current = await getStatus();
				if (cancelled) return;
				status = current;
				if (!current.ready) return;
				const [s, r, c, rk] = await Promise.all([
					getStats(filters),
					getHistogram('histogram', filters),
					getHistogram('histogram/cost/by-provider', filters),
					getRankings(filters)
				]);
				if (cancelled) return;
				stats = s;
				requests = r;
				costByProvider = c;
				rankings = rk;
			} catch (err) {
				if (!cancelled) console.error('Failed to load gateway usage:', err);
			} finally {
				if (!cancelled) loading = false;
			}
		})();
		return () => {
			cancelled = true;
		};
	});

	function handleDateRangeChange(range: DateRange) {
		const url = new URL(page.url);
		url.searchParams.set(START_PARAM, range.start?.toISOString() ?? '');
		url.searchParams.set(END_PARAM, range.end?.toISOString() ?? '');
		goto(url, { noScroll: true, keepFocus: true });
	}

	let ready = $derived(!!status?.ready);

	let timelineData = $derived(
		(requests?.buckets ?? []).flatMap((b) => [
			{ category: 'Success', timestamp: b.timestamp, count: b.success ?? 0 },
			{ category: 'Error', timestamp: b.timestamp, count: b.error ?? 0 }
		])
	);
	let hasRequests = $derived(timelineData.some((d) => d.count > 0));

	let providerCost = $derived.by(() => {
		const totals: Record<string, number> = {};
		for (const b of costByProvider?.buckets ?? []) {
			for (const [p, v] of Object.entries(b.by_provider ?? {})) {
				totals[p] = (totals[p] ?? 0) + (typeof v === 'number' ? v : 0);
			}
		}
		return Object.entries(totals)
			.map(([provider, cost]) => ({ provider: providerLabel(provider), cost }))
			.sort((a, b) => b.cost - a.cost);
	});

	let topModels = $derived(
		[...rankings]
			.sort((a, b) => b.total_requests - a.total_requests)
			.slice(0, 10)
			.map((m) => ({
				...m,
				id: `${m.provider}/${m.model}`,
				providerName: providerLabel(m.provider),
				requestsLabel: formatNumber(m.total_requests),
				successLabel: `${m.success_rate.toFixed(1)}%`,
				tokensLabel: formatNumber(m.total_tokens),
				costLabel: formatCost(m.total_cost),
				latencyLabel: `${Math.round(m.avg_latency * 1000)} ms`
			}))
	);

	let statTiles = $derived([
		{
			id: 'requests',
			label: 'Requests',
			value: stats?.total_requests ?? 0,
			format: (n: number) => formatNumber(n)
		},
		{
			id: 'success-rate',
			label: 'Success rate',
			value: stats?.success_rate ?? 0,
			format: (n: number) => `${n.toFixed(1)}%`
		},
		{
			id: 'latency',
			label: 'Avg latency',
			value: (stats?.average_latency ?? 0) * 1000,
			format: (n: number) => `${Math.round(n)} ms`
		},
		{
			id: 'tokens',
			label: 'Tokens',
			value: stats?.total_tokens ?? 0,
			format: (n: number) => formatNumber(n)
		},
		{
			id: 'cost',
			label: 'Cost',
			value: stats?.total_cost ?? 0,
			format: (n: number) => formatCost(n)
		}
	]);

	const duration = PAGE_TRANSITION_DURATION;
</script>

<div class="flex flex-col gap-4 pb-8" in:fade={{ duration }}>
	<GatewayStatusBanner {status} />

	<div class="flex w-full justify-end">
		<AuditLogCalendar start={startDate} end={endDate} onChange={handleDateRangeChange} />
	</div>

	{#if !status || ready}
		<div class="@container paper min-w-0 gap-0 p-0">
			<div class="grid w-full grid-cols-2 gap-0 @md:grid-cols-5">
				{#each statTiles as tile (tile.id)}
					<div
						class="border-base-300 col-span-1 my-2 flex min-w-0 px-2 not-last:border-r @max-md:nth-[2n]:border-r-0"
					>
						<div class="w-full min-w-0 p-2 leading-none">
							<div class="text-muted-content mb-1 text-[11px] tracking-wide @md:text-xs">
								{tile.label}
							</div>
							{#if loading}
								<div class="skeleton h-6 w-16 rounded-md"></div>
							{:else}
								<div class="text-lg font-semibold tracking-tight tabular-nums @md:text-xl">
									<TweenedMetric target={tile.value} format={tile.format} />
								</div>
							{/if}
						</div>
					</div>
				{/each}
			</div>
		</div>

		<div class="grid grid-cols-1 gap-4 xl:grid-cols-3">
			<div class="paper flex min-h-0 flex-col gap-0 overflow-hidden p-0 xl:col-span-2">
				{@render cardHeader('Requests')}
				<div class="w-full shrink-0 p-4">
					{#if loading}
						<div class="skeleton h-48 w-full rounded-md"></div>
					{:else if hasRequests}
						<StackedTimeline
							start={startDate}
							end={endDate}
							data={timelineData}
							categoryKey="category"
							dateKey="timestamp"
							primaryValueKey="count"
							class="h-48"
						/>
					{:else}
						{@render empty()}
					{/if}
				</div>
			</div>
			<div class="paper flex min-h-0 flex-col gap-0 overflow-hidden p-0">
				{@render cardHeader('Cost by provider')}
				<div class="w-full shrink-0 p-4">
					{#if loading}
						<div class="skeleton h-48 w-full rounded-md"></div>
					{:else if providerCost.length}
						{@const maxCost = Math.max(...providerCost.map((p) => p.cost))}
						<div class="flex flex-col gap-3">
							{#each providerCost as p (p.provider)}
								<div class="flex flex-col gap-1">
									<div class="flex justify-between text-xs">
										<span>{p.provider}</span>
										<span class="font-medium">{formatCost(p.cost)}</span>
									</div>
									<div class="bg-base-200 dark:bg-base-300 h-2 w-full rounded-full">
										<div
											class="bg-primary h-2 rounded-full"
											style={`width: ${maxCost ? Math.max(2, (p.cost / maxCost) * 100) : 0}%`}
										></div>
									</div>
								</div>
							{/each}
						</div>
					{:else}
						{@render empty()}
					{/if}
				</div>
			</div>
		</div>

		<div class="paper flex min-h-0 flex-col gap-0 overflow-hidden p-0">
			{@render cardHeader('Top models')}
			{#if loading}
				<Skeleton type="table" count={3} class="p-4" />
			{:else}
				<Table
					data={topModels}
					fields={[
						'model',
						'providerName',
						'requestsLabel',
						'successLabel',
						'tokensLabel',
						'costLabel',
						'latencyLabel'
					]}
					headers={[
						{ property: 'model', title: 'Model' },
						{ property: 'providerName', title: 'Provider' },
						{ property: 'requestsLabel', title: 'Requests' },
						{ property: 'successLabel', title: 'Success' },
						{ property: 'tokensLabel', title: 'Tokens' },
						{ property: 'costLabel', title: 'Cost' },
						{ property: 'latencyLabel', title: 'Latency' }
					]}
					classes={{ root: 'rounded-none shadow-none' }}
					noDataMessage="No requests in this period."
				/>
			{/if}
		</div>
	{/if}
</div>

{#snippet cardHeader(label: string)}
	<h5
		class="dark:border-base-400 border-base-300 shrink-0 rounded-t-md border-b px-4 py-2 text-xs font-medium uppercase"
	>
		{label}
	</h5>
{/snippet}

{#snippet empty()}
	<div class="text-muted-content flex h-48 items-center justify-center text-sm font-light">
		No requests in this period.
	</div>
{/snippet}
