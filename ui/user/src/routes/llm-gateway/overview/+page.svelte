<script lang="ts">
	import Layout from '$lib/components/Layout.svelte';
	import TweenedMetric from '$lib/components/TweenedMetric.svelte';
	import StackedTimeline from '$lib/components/graph/StackedTimeline.svelte';
	import GatewayConnect from '$lib/components/llm-gateway/GatewayConnect.svelte';
	import GatewayStatusBanner from '$lib/components/llm-gateway/GatewayStatusBanner.svelte';
	import Loading from '$lib/icons/Loading.svelte';
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
	import { onMount } from 'svelte';
	import { twMerge } from 'tailwind-merge';

	const ranges = [
		{ id: '1h', label: 'Last hour', ms: 60 * 60 * 1000 },
		{ id: '24h', label: 'Last 24 hours', ms: 24 * 60 * 60 * 1000 },
		{ id: '7d', label: 'Last 7 days', ms: 7 * 24 * 60 * 60 * 1000 },
		{ id: '30d', label: 'Last 30 days', ms: 30 * 24 * 60 * 60 * 1000 }
	];

	let range = $state('24h');
	let status = $state<GatewayStatus>();
	let stats = $state<LogStats>();
	let requests = $state<Histogram>();
	let costByProvider = $state<Histogram>();
	let rankings = $state<ModelRanking[]>([]);
	let loading = $state(true);
	let obotURL = $state('');
	let now = $state(new Date());

	let start = $derived(new Date(now.getTime() - (ranges.find((r) => r.id === range)?.ms ?? 0)));

	async function load() {
		loading = true;
		now = new Date();
		const filters: LogFilters = {
			start_time: start.toISOString(),
			end_time: now.toISOString(),
			objects: INFERENCE_OBJECTS
		};
		try {
			status = await getStatus();
			if (!status.ready) return;
			[stats, requests, costByProvider, rankings] = await Promise.all([
				getStats(filters),
				getHistogram('histogram', filters),
				getHistogram('histogram/cost/by-provider', filters),
				getRankings(filters)
			]);
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		obotURL = window.location.origin;
		load();
	});

	let timelineData = $derived(
		(requests?.buckets ?? []).flatMap((b) => [
			{ category: 'Success', timestamp: b.timestamp, count: b.success ?? 0 },
			{ category: 'Error', timestamp: b.timestamp, count: b.error ?? 0 }
		])
	);

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
		[...rankings].sort((a, b) => b.total_requests - a.total_requests).slice(0, 8)
	);
</script>

<svelte:head>
	<title>Obot | LLM Gateway</title>
</svelte:head>

<Layout title="LLM Gateway" classes={{ childrenContainer: 'max-w-none' }}>
	{#snippet rightNavActions()}
		<div class="flex items-center gap-2">
			<select class="select select-sm" bind:value={range} onchange={load} aria-label="Time range">
				{#each ranges as r (r.id)}
					<option value={r.id}>{r.label}</option>
				{/each}
			</select>
		</div>
	{/snippet}

	<div class="flex flex-col gap-6">
		<p class="text-muted-content max-w-4xl text-sm">
			Obot's LLM gateway, powered by <a
				href="https://github.com/maximhq/bifrost"
				target="_blank"
				rel="noreferrer"
				class="text-link">Bifrost</a
			>, puts every model behind one endpoint with Obot authentication, access policies and audit
			logs, plus load balancing, automatic fallbacks, budgets, rate limits, routing rules and
			caching.
		</p>

		<GatewayStatusBanner {status} />

		<div class="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-5">
			{@render tile('Requests', stats?.total_requests ?? 0, (n) => formatNumber(n))}
			{@render tile('Success rate', stats?.success_rate ?? 0, (n) => `${n.toFixed(1)}%`)}
			{@render tile(
				'Avg latency',
				(stats?.average_latency ?? 0) * 1000,
				(n) => `${Math.round(n)} ms`
			)}
			{@render tile('Tokens', stats?.total_tokens ?? 0, (n) => formatNumber(n))}
			{@render tile('Cost', stats?.total_cost ?? 0, (n) => formatCost(n))}
		</div>

		<div class="grid grid-cols-1 gap-4 xl:grid-cols-3">
			<div class="paper flex min-h-0 flex-col p-0 xl:col-span-2">
				{@render cardHeader('Requests')}
				<div class="h-56 p-4">
					{#if loading}
						<div class="flex h-full items-center justify-center"><Loading class="size-6" /></div>
					{:else if timelineData.length}
						<StackedTimeline
							{start}
							end={now}
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
			<div class="paper flex min-h-0 flex-col p-0">
				{@render cardHeader('Cost by provider')}
				<div class="h-56 p-4">
					{#if loading}
						<div class="flex h-full items-center justify-center"><Loading class="size-6" /></div>
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

		<div class="grid grid-cols-1 gap-4 xl:grid-cols-2">
			<div class="paper flex flex-col p-0">
				{@render cardHeader('Top models')}
				<div class="overflow-x-auto p-2">
					{#if topModels.length}
						<table class="table-sm table w-full text-sm">
							<thead>
								<tr class="text-muted-content text-xs">
									<th class="text-left">Model</th>
									<th class="text-right">Requests</th>
									<th class="text-right">Success</th>
									<th class="text-right">Tokens</th>
									<th class="text-right">Cost</th>
									<th class="text-right">Latency</th>
								</tr>
							</thead>
							<tbody>
								{#each topModels as m (m.provider + m.model)}
									<tr>
										<td>
											<span class="font-medium">{m.model}</span>
											<span class="text-muted-content text-xs">{providerLabel(m.provider)}</span>
										</td>
										<td class="text-right">{formatNumber(m.total_requests)}</td>
										<td class="text-right">{m.success_rate.toFixed(1)}%</td>
										<td class="text-right">{formatNumber(m.total_tokens)}</td>
										<td class="text-right">{formatCost(m.total_cost)}</td>
										<td class="text-right">{Math.round(m.avg_latency * 1000)} ms</td>
									</tr>
								{/each}
							</tbody>
						</table>
					{:else}
						<div class="p-4">{@render empty()}</div>
					{/if}
				</div>
			</div>
			<div class="paper flex flex-col p-0">
				{@render cardHeader('Connect a client')}
				<div class="p-4">
					{#if obotURL}
						<GatewayConnect {obotURL} />
					{/if}
				</div>
			</div>
		</div>
	</div>
</Layout>

{#snippet tile(label: string, value: number, format: (n: number) => string)}
	<div class="paper flex flex-col gap-1 p-4">
		<span class="text-muted-content text-xs font-medium uppercase">{label}</span>
		<span class={twMerge('text-2xl font-semibold', loading && 'opacity-50')}>
			<TweenedMetric target={value} holdAtZero={loading} {format} />
		</span>
	</div>
{/snippet}

{#snippet cardHeader(label: string)}
	<h5
		class="dark:border-base-400 border-base-300 shrink-0 rounded-t-md border-b px-4 py-2 text-xs font-medium uppercase"
	>
		{label}
	</h5>
{/snippet}

{#snippet empty()}
	<div class="text-muted-content flex h-full items-center justify-center text-sm">
		No requests in this period.
	</div>
{/snippet}
