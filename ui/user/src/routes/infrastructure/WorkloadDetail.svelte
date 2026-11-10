<script lang="ts">
	import Table from '$lib/components/table/Table.svelte';
	import { m } from '$lib/i18n';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		getMCPServerLogs,
		type InfraWorkload,
		type PodLogs
	} from '$lib/services/admin/infrastructure';
	import { formatTimeAgo } from '$lib/time';
	import { resolve } from '$lib/url';
	import HealthBadge from './HealthBadge.svelte';
	import { ExternalLink, RefreshCw, X } from '@lucide/svelte';
	import { twMerge } from 'tailwind-merge';

	interface Props {
		workload: InfraWorkload;
		showLogs?: boolean;
		onClose?: () => void;
	}

	let { workload, showLogs = false, onClose }: Props = $props();

	let logs = $state<PodLogs>();
	let logsError = $state('');
	let loadingLogs = $state(false);
	let previous = $state(false);
	let logFilter = $state('');

	const pods = $derived(workload.pods.map((p) => ({ ...p, id: p.name })));
	const events = $derived(workload.events.map((e, i) => ({ ...e, id: i })));
	const filteredLogLines = $derived.by(() => {
		const terms = logFilter.toLowerCase().split(/\s+/).filter(Boolean);
		const lines = logs?.lines ?? [];
		if (terms.length === 0) return lines;
		return lines.filter((l) => {
			const lower = l.toLowerCase();
			return terms.every((t) => lower.includes(t));
		});
	});

	async function loadLogs() {
		loadingLogs = true;
		logsError = '';
		try {
			logs = await getMCPServerLogs(workload.name, { tail: 500, previous });
		} catch (err) {
			logs = undefined;
			logsError = err instanceof Error ? err.message : String(err);
		} finally {
			loadingLogs = false;
		}
	}

	function resources() {
		const req = [workload.cpuRequest, workload.memoryRequest].filter(Boolean).join(' / ');
		const lim = [workload.cpuLimit, workload.memoryLimit].filter(Boolean).join(' / ');
		return { req: req || '—', lim: lim || '—' };
	}
</script>

<div class="paper flex flex-col gap-4 p-4">
	<div class="flex flex-wrap items-start justify-between gap-2">
		<div class="flex flex-col gap-1">
			<div class="flex items-center gap-2">
				<h3 class="text-lg font-semibold">{workload.displayName || workload.name}</h3>
				<HealthBadge health={workload.health} />
			</div>
			<p class="text-muted-content text-xs font-mono">{workload.namespace}/{workload.name}</p>
			{#if workload.healthReason}
				<p
					class={twMerge(
						'text-sm',
						workload.health === 'failing' ? 'text-error' : 'text-muted-content'
					)}
				>
					{workload.healthReason}
				</p>
			{/if}
		</div>
		<div class="flex items-center gap-2">
			{#if workload.link}
				<a class="btn btn-secondary btn-sm" href={resolve(workload.link as `/${string}`)}>
					<ExternalLink class="size-4" />
					{workload.kind === 'claudeWorkerPool'
						? m.infrastructure_view_environment()
						: workload.vmcpID
							? m.infrastructure_view_vmcp()
							: workload.catalogEntryID
								? m.infrastructure_view_catalog_entry()
								: m.infrastructure_view_server()}
				</a>
			{/if}
			{#if onClose}
				<button class="icon-button" onclick={onClose} aria-label={m.infrastructure_close()}>
					<X class="size-4" />
				</button>
			{/if}
		</div>
	</div>

	<dl class="grid grid-cols-2 gap-x-6 gap-y-2 text-sm md:grid-cols-4">
		<div>
			<dt class="text-muted-content text-xs">{m.infrastructure_col_ready()}</dt>
			<dd>{workload.readyReplicas}/{workload.replicas}</dd>
		</div>
		<div>
			<dt class="text-muted-content text-xs">{m.infrastructure_col_restarts()}</dt>
			<dd>{workload.restarts}</dd>
		</div>
		<div>
			<dt class="text-muted-content text-xs">{m.infrastructure_requests()}</dt>
			<dd>{resources().req}</dd>
		</div>
		<div>
			<dt class="text-muted-content text-xs">{m.infrastructure_limits()}</dt>
			<dd>{resources().lim}</dd>
		</div>
		{#if workload.image}
			<div class="col-span-2 md:col-span-4">
				<dt class="text-muted-content text-xs">{m.infrastructure_col_image()}</dt>
				<dd class="font-mono text-xs break-all">{workload.image}</dd>
			</div>
		{/if}
		{#if workload.userName || workload.vmcpName || workload.catalogEntryName}
			<div class="col-span-2 md:col-span-4 flex flex-wrap gap-x-6 gap-y-1">
				{#if workload.userName}
					<span
						><span class="text-muted-content text-xs">{m.infrastructure_col_user()}:</span>
						{workload.userName}</span
					>
				{/if}
				{#if workload.vmcpName}
					<span
						><span class="text-muted-content text-xs">vMCP:</span>
						{workload.vmcpName}</span
					>
				{/if}
				{#if workload.catalogEntryName}
					<span
						><span class="text-muted-content text-xs">{m.infrastructure_catalog_entry()}:</span>
						{workload.catalogEntryName}</span
					>
				{/if}
			</div>
		{/if}
	</dl>

	<div class="flex flex-col gap-2">
		<h4 class="text-sm font-semibold">{m.infrastructure_pods()}</h4>
		<Table
			data={pods}
			fields={['name', 'status', 'ready', 'restarts', 'lastTermination', 'created']}
			headers={[
				{ title: m.infrastructure_col_pod(), property: 'name' },
				{ title: m.infrastructure_col_status(), property: 'status' },
				{ title: m.infrastructure_col_ready(), property: 'ready' },
				{ title: m.infrastructure_col_restarts(), property: 'restarts' },
				{ title: m.infrastructure_col_last_termination(), property: 'lastTermination' },
				{ title: m.infrastructure_col_age(), property: 'created' }
			]}
			noDataMessage={m.infrastructure_no_pods()}
			classes={{ root: 'shadow-none' }}
		>
			{#snippet onRenderColumn(property, d)}
				{#if property === 'name'}
					<span class="font-mono text-xs">{d.name}</span>
				{:else if property === 'status'}
					<div class="flex flex-col">
						<span class={d.status === 'Running' ? '' : 'text-warning'}>{d.status}</span>
						{#if d.message}
							<span class="text-muted-content text-xs line-clamp-2">{d.message}</span>
						{/if}
					</div>
				{:else if property === 'created'}
					{formatTimeAgo(d.created).relativeTime}
				{:else if property === 'lastTermination'}
					<span class="text-xs">{d.lastTermination || '—'}</span>
				{:else}
					{d[property as keyof typeof d] ?? '—'}
				{/if}
			{/snippet}
		</Table>
	</div>

	<div class="flex flex-col gap-2">
		<h4 class="text-sm font-semibold">{m.infrastructure_events()}</h4>
		<Table
			data={events}
			fields={['type', 'reason', 'message', 'object', 'lastSeen']}
			headers={[
				{ title: m.infrastructure_col_type(), property: 'type' },
				{ title: m.infrastructure_col_reason(), property: 'reason' },
				{ title: m.infrastructure_col_message(), property: 'message' },
				{ title: m.infrastructure_col_object(), property: 'object' },
				{ title: m.infrastructure_col_last_seen(), property: 'lastSeen' }
			]}
			noDataMessage={m.infrastructure_no_events()}
			classes={{ root: 'shadow-none' }}
		>
			{#snippet onRenderColumn(property, d)}
				{#if property === 'type'}
					<span class={d.type === 'Warning' ? 'text-warning font-medium' : 'text-muted-content'}
						>{d.type}</span
					>
				{:else if property === 'message'}
					<span class="text-xs whitespace-pre-wrap break-words"
						>{d.message}{#if d.count && d.count > 1}
							<span class="text-muted-content"> (×{d.count})</span>{/if}</span
					>
				{:else if property === 'object'}
					<span class="font-mono text-xs">{d.object}</span>
				{:else if property === 'lastSeen'}
					{formatTimeAgo(d.lastSeen).relativeTime}
				{:else}
					{d[property as keyof typeof d] ?? ''}
				{/if}
			{/snippet}
		</Table>
	</div>

	{#if showLogs}
		<div class="flex flex-col gap-2">
			<div class="flex flex-wrap items-center justify-between gap-2">
				<h4 class="text-sm font-semibold">
					{m.infrastructure_logs()}
					{#if logs}
						<span class="text-muted-content font-mono text-xs font-normal"
							>{logs.pod}/{logs.container}</span
						>
					{/if}
				</h4>
				<div class="flex flex-wrap items-center gap-3">
					<label class="flex items-center gap-1.5 text-xs">
						<input type="checkbox" class="checkbox checkbox-xs" bind:checked={previous} />
						{m.infrastructure_previous_container()}
					</label>
					{#if logs}
						<input
							class="text-input-filled h-8 w-48 text-xs"
							placeholder={m.infrastructure_filter_lines()}
							bind:value={logFilter}
						/>
					{/if}
					<button class="btn btn-secondary btn-sm" onclick={loadLogs} disabled={loadingLogs}>
						{#if loadingLogs}
							<Loading class="size-4" />
						{:else}
							<RefreshCw class="size-4" />
						{/if}
						{logs ? m.infrastructure_reload_logs() : m.infrastructure_load_logs()}
					</button>
				</div>
			</div>
			{#if logsError}
				<p class="text-error text-sm">{logsError}</p>
			{/if}
			{#if logs}
				<pre
					class="bg-base-200 dark:bg-base-300 max-h-[28rem] overflow-auto rounded-md p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all">{filteredLogLines.length
						? filteredLogLines.join('\n')
						: m.infrastructure_no_log_lines()}</pre>
			{/if}
		</div>
	{/if}
</div>
