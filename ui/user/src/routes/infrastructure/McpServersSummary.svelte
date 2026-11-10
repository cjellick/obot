<script lang="ts">
	import Table from '$lib/components/table/Table.svelte';
	import { m } from '$lib/i18n';
	import type { InfraSection, InfraWorkload } from '$lib/services/admin/infrastructure';
	import { formatTimeAgo } from '$lib/time';
	import { goto, resolve } from '$lib/url';
	import HealthBadge from './HealthBadge.svelte';
	import { ArrowRight } from '@lucide/svelte';

	let {
		section,
		limit = 8,
		onSelect
	}: { section: InfraSection; limit?: number; onSelect?: (name: string) => void } = $props();

	const order: Record<string, number> = { failing: 0, starting: 1, stopped: 2, healthy: 3 };

	const workloads = $derived(
		[...section.workloads].sort(
			(a, b) =>
				(order[a.health] ?? 4) - (order[b.health] ?? 4) ||
				(a.displayName || a.name).localeCompare(b.displayName || b.name)
		)
	);
	const shown = $derived(workloads.slice(0, limit).map((w) => ({ ...w, id: w.name })));

	const breakdown = $derived.by(() => {
		const c = { healthy: 0, starting: 0, failing: 0, stopped: 0 };
		for (const w of section.workloads) {
			if (w.health in c) c[w.health as keyof typeof c]++;
		}
		const total = section.workloads.length;
		return [
			{
				key: 'healthy',
				count: c.healthy,
				bar: 'bg-success',
				label: m.infrastructure_health_healthy()
			},
			{
				key: 'starting',
				count: c.starting,
				bar: 'bg-warning',
				label: m.infrastructure_health_starting()
			},
			{
				key: 'failing',
				count: c.failing,
				bar: 'bg-error',
				label: m.infrastructure_health_failing()
			},
			{
				key: 'stopped',
				count: c.stopped,
				bar: 'bg-base-400',
				label: m.infrastructure_health_stopped()
			}
		]
			.filter((s) => s.key !== 'stopped' || s.count > 0)
			.map((s) => ({ ...s, pct: total ? (s.count / total) * 100 : 0 }));
	});

	function owner(w: InfraWorkload) {
		if (w.kind === 'vmcp') return 'vMCP';
		if (w.kind === 'system') return m.infrastructure_kind_system();
		if (w.vmcpName) return `vMCP: ${w.vmcpName}`;
		return w.userName || w.catalogEntryName || '—';
	}

	function openServer(e: MouseEvent, name: string) {
		if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
		e.preventDefault();
		onSelect?.(name);
		goto('/infrastructure?view=mcp');
	}

	function shortImage(image?: string) {
		if (!image) return '—';
		return image.split('/').pop() ?? image;
	}
</script>

<section class="flex flex-col gap-2" aria-labelledby="infra-overview-mcp">
	<div class="flex items-center justify-between gap-2">
		<h2 id="infra-overview-mcp" class="text-lg font-semibold">{m.infrastructure_area_mcp()}</h2>
		<a
			class="link text-primary flex items-center gap-1 text-sm"
			href={resolve('/infrastructure?view=mcp')}
		>
			{m.infrastructure_view_all_mcp({ count: section.workloads.length })}
			<ArrowRight class="size-4" />
		</a>
	</div>

	{#if section.workloads.length === 0}
		<div class="paper text-muted-content p-4 text-sm">{m.infrastructure_overview_no_mcp()}</div>
	{:else}
		<div class="paper flex flex-col gap-2 p-4">
			<div
				class="bg-base-300 flex h-2 w-full overflow-hidden rounded-full"
				role="img"
				aria-label={m.infrastructure_health_breakdown()}
			>
				{#each breakdown as s (s.key)}
					{#if s.count > 0}
						<div class="{s.bar} h-full" style="width: {s.pct}%" title="{s.label}: {s.count}"></div>
					{/if}
				{/each}
			</div>
			<div class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
				{#each breakdown as s (s.key)}
					<span class="flex items-center gap-1.5">
						<span class="{s.bar} size-2 rounded-full"></span>
						<span class="text-muted-content">{s.label}</span>
						<span class="font-medium">{s.count}</span>
					</span>
				{/each}
			</div>
		</div>

		<Table
			data={shown}
			fields={['displayName', 'owner', 'health', 'ready', 'restarts', 'created', 'image']}
			headers={[
				{ title: m.infrastructure_col_name(), property: 'displayName' },
				{ title: m.infrastructure_col_owner(), property: 'owner' },
				{ title: m.infrastructure_col_health(), property: 'health' },
				{ title: m.infrastructure_col_ready(), property: 'ready' },
				{ title: m.infrastructure_col_restarts(), property: 'restarts' },
				{ title: m.infrastructure_col_age(), property: 'created' },
				{ title: m.infrastructure_col_image(), property: 'image' }
			]}
			classes={{ root: 'shadow-none' }}
		>
			{#snippet onRenderColumn(property, d)}
				{#if property === 'displayName'}
					<a
						class="hover:text-primary font-medium"
						href={resolve('/infrastructure?view=mcp')}
						onclick={(e) => openServer(e, d.name)}>{d.displayName || d.name}</a
					>
				{:else if property === 'owner'}
					<span class="text-sm">{owner(d)}</span>
				{:else if property === 'health'}
					<HealthBadge health={d.health} />
				{:else if property === 'ready'}
					{d.readyReplicas}/{d.replicas}
				{:else if property === 'restarts'}
					<span class={d.restarts > 0 ? 'text-warning font-medium' : ''}>{d.restarts}</span>
				{:else if property === 'created'}
					{formatTimeAgo(d.created).relativeTime}
				{:else if property === 'image'}
					<span class="text-muted-content font-mono text-xs" title={d.image}
						>{shortImage(d.image)}</span
					>
				{:else}
					{d[property as keyof typeof d] ?? '—'}
				{/if}
			{/snippet}
		</Table>
	{/if}
</section>
