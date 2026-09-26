<script lang="ts">
	import { resolve } from '$app/paths';
	import Layout from '$lib/components/Layout.svelte';
	import GatewayStatusBanner from '$lib/components/llm-gateway/GatewayStatusBanner.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		formatCost,
		formatNumber,
		getLog,
		getLogFilterData,
		getStats,
		getStatus,
		INFERENCE_OBJECTS,
		listLogs,
		listProviders,
		type GatewayLog,
		type GatewayStatus,
		type LogFilterData,
		type LogFilters,
		type LogStats
	} from '$lib/services/llm-gateway/gateway';
	import { providerLabel } from '$lib/services/llm-gateway/providers';
	import { ChevronLeft, ChevronRight, RefreshCw, X } from '@lucide/svelte';
	import { onMount } from 'svelte';
	import { twMerge } from 'tailwind-merge';

	const pageSize = 50;
	const ranges = [
		{ id: '1h', label: 'Last hour', ms: 3600e3 },
		{ id: '24h', label: 'Last 24 hours', ms: 86400e3 },
		{ id: '7d', label: 'Last 7 days', ms: 7 * 86400e3 },
		{ id: '30d', label: 'Last 30 days', ms: 30 * 86400e3 }
	];

	let status = $state<GatewayStatus>();
	let logs = $state<GatewayLog[]>([]);
	let total = $state(0);
	let stats = $state<LogStats>();
	let filterData = $state<LogFilterData>();
	let providers = $state<string[]>([]);
	let loading = $state(true);
	let offset = $state(0);

	let range = $state('24h');
	let provider = $state('');
	let model = $state('');
	let statusFilter = $state('');
	let user = $state('');
	let search = $state('');
	let showListModels = $state(false);

	let selected = $state<GatewayLog>();
	let detailLoading = $state(false);

	let users = $derived(filterData?.metadata_keys?.['obot-user-name'] ?? []);

	function filters(): LogFilters {
		const r = ranges.find((x) => x.id === range)!;
		const f: LogFilters = {
			start_time: new Date(Date.now() - r.ms).toISOString(),
			objects: showListModels ? undefined : INFERENCE_OBJECTS
		};
		if (provider) f.providers = [provider];
		if (model) f.models = [model];
		if (statusFilter) f.status = [statusFilter];
		if (search.trim()) f.content_search = search.trim();
		if (user) f.metadata = { 'obot-user-name': user };
		return f;
	}

	async function load(resetOffset = false) {
		if (resetOffset) offset = 0;
		loading = true;
		try {
			status = await getStatus();
			if (!status.ready) return;
			const f = filters();
			const [resp, s] = await Promise.all([
				listLogs(f, { limit: pageSize, offset }),
				getStats(f).catch(() => undefined)
			]);
			logs = resp.logs;
			total = resp.total;
			stats = s ?? resp.stats;
		} finally {
			loading = false;
		}
	}

	onMount(async () => {
		await load();
		if (status?.ready) {
			[filterData, providers] = await Promise.all([
				getLogFilterData(),
				listProviders().then((ps) => ps.map((p) => p.name))
			]);
		}
	});

	async function openLog(log: GatewayLog) {
		selected = log;
		detailLoading = true;
		try {
			selected = await getLog(log.id);
		} finally {
			detailLoading = false;
		}
	}

	function statusClass(s: string) {
		return s === 'success'
			? 'bg-success/15 text-success'
			: s === 'error'
				? 'bg-error/15 text-error'
				: 'bg-warning/15 text-warning';
	}

	function pretty(v: unknown) {
		if (v === undefined || v === null || v === '') return '';
		if (typeof v === 'string') {
			try {
				return JSON.stringify(JSON.parse(v), null, 2);
			} catch {
				return v;
			}
		}
		return JSON.stringify(v, null, 2);
	}
</script>

<svelte:head>
	<title>Obot | LLM Request Logs</title>
</svelte:head>

<Layout title="Request Logs" classes={{ childrenContainer: 'max-w-none' }}>
	{#snippet rightNavActions()}
		<IconButton tooltip={{ text: 'Refresh' }} onclick={() => load()}>
			<RefreshCw class="size-4" />
		</IconButton>
	{/snippet}

	<div class="flex flex-col gap-4">
		<p class="text-muted-content max-w-4xl text-sm">
			Every request the gateway routed, with the provider and key that served it, retries and
			fallbacks, tokens, cost and latency. Obot's own <a
				href={resolve('/audit-logs?view=llm')}
				class="text-link">LLM audit log</a
			> records the same requests from the caller's side.
		</p>

		<GatewayStatusBanner {status} showNoProviders={false} />

		<div class="flex flex-wrap items-center gap-2">
			<select
				class="select select-sm"
				bind:value={range}
				onchange={() => load(true)}
				aria-label="Time range"
			>
				{#each ranges as r (r.id)}<option value={r.id}>{r.label}</option>{/each}
			</select>
			<select
				class="select select-sm"
				bind:value={provider}
				onchange={() => load(true)}
				aria-label="Provider"
			>
				<option value="">All providers</option>
				{#each providers as p (p)}<option value={p}>{providerLabel(p)}</option>{/each}
			</select>
			<select
				class="select select-sm"
				bind:value={model}
				onchange={() => load(true)}
				aria-label="Model"
			>
				<option value="">All models</option>
				{#each filterData?.models ?? [] as m (m)}<option value={m}>{m}</option>{/each}
			</select>
			<select
				class="select select-sm"
				bind:value={statusFilter}
				onchange={() => load(true)}
				aria-label="Status"
			>
				<option value="">Any status</option>
				<option value="success">Success</option>
				<option value="error">Error</option>
				<option value="processing">Processing</option>
			</select>
			<select
				class="select select-sm"
				bind:value={user}
				onchange={() => load(true)}
				aria-label="User"
			>
				<option value="">All users</option>
				{#each users as u (u)}<option value={u}>{u}</option>{/each}
			</select>
			<input
				class="text-input-filled h-8 w-64 text-sm"
				placeholder="Search request and response content"
				bind:value={search}
				onkeydown={(e) => e.key === 'Enter' && load(true)}
			/>
			<label class="flex items-center gap-1 text-xs">
				<input
					type="checkbox"
					class="checkbox checkbox-xs"
					bind:checked={showListModels}
					onchange={() => load(true)}
				/>
				Include model listing
			</label>
		</div>

		{#if stats}
			<div class="text-muted-content flex flex-wrap gap-6 text-xs">
				<span><b class="text-base-content">{formatNumber(stats.total_requests)}</b> requests</span>
				<span><b class="text-base-content">{stats.success_rate.toFixed(1)}%</b> success</span>
				<span
					><b class="text-base-content">{Math.round(stats.average_latency * 1000)} ms</b> avg latency</span
				>
				<span><b class="text-base-content">{formatNumber(stats.total_tokens)}</b> tokens</span>
				<span><b class="text-base-content">{formatCost(stats.total_cost)}</b> cost</span>
			</div>
		{/if}

		<div class="paper overflow-x-auto p-0">
			<table class="table-sm table w-full text-sm">
				<thead>
					<tr class="text-muted-content text-xs">
						<th class="text-left">Time</th>
						<th class="text-left">Status</th>
						<th class="text-left">User</th>
						<th class="text-left">Provider / model</th>
						<th class="text-left">Type</th>
						<th class="text-left">Key</th>
						<th class="text-right">Tokens</th>
						<th class="text-right">Cost</th>
						<th class="text-right">Latency</th>
					</tr>
				</thead>
				<tbody>
					{#if loading}
						<tr><td colspan="9" class="p-8 text-center"><Loading class="mx-auto size-6" /></td></tr>
					{:else if logs.length === 0}
						<tr
							><td colspan="9" class="text-muted-content p-8 text-center">No requests match.</td
							></tr
						>
					{:else}
						{#each logs as log (log.id)}
							<tr
								class={twMerge(
									'hover:bg-base-200 dark:hover:bg-base-300 cursor-pointer',
									selected?.id === log.id && 'bg-base-200 dark:bg-base-300'
								)}
								onclick={() => openLog(log)}
							>
								<td class="whitespace-nowrap">{new Date(log.timestamp).toLocaleString()}</td>
								<td
									><span class={`rounded-full px-2 py-0.5 text-xs ${statusClass(log.status)}`}
										>{log.status}</span
									></td
								>
								<td class="truncate"
									>{log.metadata?.['obot-user-name'] ?? log.metadata?.['obot-user-id'] ?? '—'}</td
								>
								<td>
									<span class="font-medium">{log.model || '—'}</span>
									<span class="text-muted-content text-xs">{providerLabel(log.provider)}</span>
									{#if (log.fallback_index ?? 0) > 0}
										<span class="bg-warning/15 text-warning ml-1 rounded px-1 text-xs"
											>fallback</span
										>
									{/if}
									{#if (log.number_of_retries ?? 0) > 0}
										<span class="bg-base-300 ml-1 rounded px-1 text-xs"
											>{log.number_of_retries} retries</span
										>
									{/if}
								</td>
								<td class="text-xs">{log.object}{log.stream ? ' · stream' : ''}</td>
								<td class="text-xs">{log.selected_key_name ?? '—'}</td>
								<td class="text-right">{formatNumber(log.token_usage?.total_tokens)}</td>
								<td class="text-right">{formatCost(log.cost)}</td>
								<td class="text-right"
									>{log.latency ? `${Math.round(log.latency * 1000)} ms` : '—'}</td
								>
							</tr>
						{/each}
					{/if}
				</tbody>
			</table>
		</div>

		<div class="flex items-center justify-end gap-2 text-sm">
			<span class="text-muted-content">
				{total ? `${offset + 1}–${Math.min(offset + pageSize, total)} of ${total}` : ''}
			</span>
			<IconButton
				tooltip={{ text: 'Previous page' }}
				disabled={offset === 0}
				onclick={() => {
					offset = Math.max(0, offset - pageSize);
					load();
				}}
			>
				<ChevronLeft class="size-4" />
			</IconButton>
			<IconButton
				tooltip={{ text: 'Next page' }}
				disabled={offset + pageSize >= total}
				onclick={() => {
					offset += pageSize;
					load();
				}}
			>
				<ChevronRight class="size-4" />
			</IconButton>
		</div>
	</div>
</Layout>

{#if selected}
	<div
		class="bg-base-100 dark:bg-base-200 border-base-300 dark:border-base-400 fixed top-0 right-0 z-50 flex h-dvh w-full flex-col border-l shadow-xl md:w-[42rem]"
		role="dialog"
		aria-label="Request details"
	>
		<div
			class="border-base-300 dark:border-base-400 flex items-center justify-between border-b p-4"
		>
			<div class="flex flex-col">
				<span class="font-semibold">{selected.model} · {providerLabel(selected.provider)}</span>
				<span class="text-muted-content text-xs"
					>{new Date(selected.timestamp).toLocaleString()} · {selected.id}</span
				>
			</div>
			<IconButton tooltip={{ text: 'Close' }} onclick={() => (selected = undefined)}>
				<X class="size-4" />
			</IconButton>
		</div>
		<div class="flex flex-col gap-4 overflow-y-auto p-4 text-sm">
			{#if detailLoading}<Loading class="size-5" />{/if}
			<div class="grid grid-cols-2 gap-2">
				{@render kv('Status', selected.status)}
				{@render kv('Type', `${selected.object}${selected.stream ? ' (stream)' : ''}`)}
				{@render kv(
					'User',
					selected.metadata?.['obot-user-name'] ?? selected.metadata?.['obot-user-id'] ?? '—'
				)}
				{@render kv('Key', selected.selected_key_name ?? '—')}
				{@render kv('Budget', selected.virtual_key_name ?? '—')}
				{@render kv('Routing rule', selected.routing_rule_name ?? '—')}
				{@render kv('Retries', String(selected.number_of_retries ?? 0))}
				{@render kv('Fallback', selected.fallback_index ? `#${selected.fallback_index}` : 'no')}
				{@render kv('Prompt tokens', formatNumber(selected.token_usage?.prompt_tokens))}
				{@render kv('Completion tokens', formatNumber(selected.token_usage?.completion_tokens))}
				{@render kv('Cost', formatCost(selected.cost))}
				{@render kv(
					'Latency',
					selected.latency ? `${Math.round(selected.latency * 1000)} ms` : '—'
				)}
			</div>
			{#if selected.error_details?.error?.message}
				<div class="notification-error rounded-md p-3 text-sm">
					{selected.error_details.error.message}
				</div>
			{/if}
			{@render block(
				'Input',
				selected.input_history?.length ? selected.input_history : selected.responses_input_history
			)}
			{@render block('Output', selected.output_message ?? selected.responses_output)}
			{@render block('Parameters', selected.params)}
			{@render block('Metadata', selected.metadata)}
		</div>
	</div>
{/if}

{#snippet kv(label: string, value: string)}
	<div class="flex flex-col">
		<span class="text-muted-content text-xs">{label}</span>
		<span class="truncate">{value}</span>
	</div>
{/snippet}

{#snippet block(label: string, value: unknown)}
	{@const text = pretty(value)}
	{#if text}
		<div class="flex flex-col gap-1">
			<span class="text-muted-content text-xs font-medium uppercase">{label}</span>
			<pre
				class="bg-base-200 dark:bg-base-300 max-h-80 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap">{text}</pre>
		</div>
	{/if}
{/snippet}
