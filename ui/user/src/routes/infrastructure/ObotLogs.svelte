<script lang="ts">
	import Search from '$lib/components/Search.svelte';
	import { m } from '$lib/i18n';
	import Loading from '$lib/icons/Loading.svelte';
	import { getObotLogs, type ObotLogs } from '$lib/services/admin/infrastructure';
	import { RefreshCw } from '@lucide/svelte';
	import { onDestroy, onMount } from 'svelte';
	import { twMerge } from 'tailwind-merge';

	let query = $state('');
	let level = $state('');
	let since = $state('');
	let logs = $state<ObotLogs>();
	let error = $state('');
	let loading = $state(false);
	let follow = $state(false);
	let timer: ReturnType<typeof setInterval> | undefined;
	let container = $state<HTMLElement>();

	async function load() {
		loading = true;
		try {
			logs = await getObotLogs({ query, level, since, limit: 1000 });
			error = '';
			queueMicrotask(() => {
				if (container) container.scrollTop = container.scrollHeight;
			});
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		clearInterval(timer);
		timer = follow ? setInterval(load, 5000) : undefined;
	});

	onMount(load);
	onDestroy(() => clearInterval(timer));

	function levelClass(lvl?: string) {
		switch (lvl) {
			case 'error':
				return 'text-error';
			case 'warn':
				return 'text-warning';
			case 'debug':
				return 'text-muted-content';
		}
		return '';
	}

	function time(t: string) {
		const d = new Date(t);
		return isNaN(d.getTime()) ? '' : d.toLocaleTimeString();
	}
</script>

<div class="flex flex-col gap-3">
	<div class="flex flex-wrap items-center gap-2">
		<div class="min-w-64 flex-1">
			<Search
				value={query}
				placeholder={m.infrastructure_logs_search()}
				onChange={(v) => {
					query = v;
					load();
				}}
			/>
		</div>
		<select class="text-input-filled w-36" bind:value={level} onchange={load}>
			<option value="">{m.infrastructure_level_all()}</option>
			<option value="info">{m.infrastructure_level_info()}</option>
			<option value="warn">{m.infrastructure_level_warn()}</option>
			<option value="error">{m.infrastructure_level_error()}</option>
		</select>
		<select class="text-input-filled w-40" bind:value={since} onchange={load}>
			<option value="">{m.infrastructure_since_all()}</option>
			<option value="15m">{m.infrastructure_since_15m()}</option>
			<option value="1h">{m.infrastructure_since_1h()}</option>
			<option value="6h">{m.infrastructure_since_6h()}</option>
		</select>
		<label class="flex items-center gap-1.5 text-sm">
			<input type="checkbox" class="checkbox checkbox-sm" bind:checked={follow} />
			{m.infrastructure_follow()}
		</label>
		<button class="btn btn-secondary btn-sm" onclick={load} disabled={loading}>
			{#if loading}
				<Loading class="size-4" />
			{:else}
				<RefreshCw class="size-4" />
			{/if}
			{m.infrastructure_refresh()}
		</button>
	</div>

	{#if error}
		<p class="text-error text-sm">{error}</p>
	{/if}

	{#if logs}
		<p class="text-muted-content text-xs">
			{m.infrastructure_logs_summary({
				shown: logs.lines.length,
				matched: logs.matched,
				buffered: logs.buffered,
				capacity: logs.capacity
			})}
			{#if logs.pod}
				· {m.infrastructure_logs_replica({ pod: logs.pod })}
			{/if}
		</p>
		<div
			bind:this={container}
			class="bg-base-200 dark:bg-base-300 max-h-[65vh] overflow-auto rounded-md p-3 font-mono text-xs leading-relaxed"
		>
			{#each logs.lines as line (line.seq)}
				<div class={twMerge('whitespace-pre-wrap break-all', levelClass(line.level))}>
					<span class="text-muted-content select-none">{time(line.time)}</span>
					{line.text}
				</div>
			{:else}
				<p class="text-muted-content">{m.infrastructure_no_log_lines()}</p>
			{/each}
		</div>
	{/if}
</div>
