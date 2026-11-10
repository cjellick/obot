<script lang="ts">
	import TabLayout from '$lib/components/TabLayout.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import { m } from '$lib/i18n';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		getInfrastructure,
		type InfraSection,
		type InfraWorkload,
		type Infrastructure
	} from '$lib/services/admin/infrastructure';
	import { formatTimeAgo } from '$lib/time';
	import { resolve } from '$lib/url';
	import ClaudeSummary from './ClaudeSummary.svelte';
	import HealthBadge from './HealthBadge.svelte';
	import McpServersSummary from './McpServersSummary.svelte';
	import ObotLogs from './ObotLogs.svelte';
	import WorkloadDetail from './WorkloadDetail.svelte';
	import { CircleAlert, CircleCheck, RefreshCw, TriangleAlert } from '@lucide/svelte';
	import { onMount } from 'svelte';

	let infra = $state<Infrastructure>();
	let error = $state('');
	let loading = $state(false);
	let selected = $state<{ area: 'mcp' | 'claude' | 'obot'; name: string }>();

	async function load() {
		loading = true;
		try {
			infra = await getInfrastructure();
			error = '';
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			loading = false;
		}
	}

	onMount(load);

	const selectedWorkload = $derived.by(() => {
		if (!infra || !selected) return undefined;
		const section = { mcp: infra.mcpServers, claude: infra.claude, obot: infra.obot }[
			selected.area
		];
		return section.workloads.find((w) => w.name === selected?.name);
	});

	function rows(section?: InfraSection) {
		return (section?.workloads ?? []).map((w) => ({ ...w, id: w.name }));
	}

	function counts(section?: InfraSection) {
		const out = { total: 0, healthy: 0, failing: 0, starting: 0, stopped: 0 };
		for (const w of section?.workloads ?? []) {
			out.total++;
			out[w.health as 'healthy' | 'failing' | 'starting' | 'stopped']++;
		}
		return out;
	}

	function resources(w: InfraWorkload) {
		const req = [w.cpuRequest, w.memoryRequest].filter(Boolean).join(' / ');
		const lim = [w.cpuLimit, w.memoryLimit].filter(Boolean).join(' / ');
		return { req: req || '—', lim: lim || '—' };
	}

	function kindLabel(w: InfraWorkload) {
		switch (w.kind) {
			case 'vmcp':
				return 'vMCP';
			case 'system':
				return m.infrastructure_kind_system();
			default:
				return w.vmcpName ? `vMCP: ${w.vmcpName}` : (w.catalogEntryName ?? '');
		}
	}

	function areaLabel(area: string) {
		return (
			{
				obot: m.infrastructure_area_obot(),
				mcp: m.infrastructure_area_mcp(),
				claude: m.infrastructure_area_claude()
			}[area] ?? area
		);
	}

	function select(area: 'mcp' | 'claude' | 'obot', name: string) {
		selected = selected?.area === area && selected.name === name ? undefined : { area, name };
	}
</script>

<svelte:head><title>Obot | {m.nav_infrastructure()}</title></svelte:head>

<TabLayout
	title={m.nav_infrastructure()}
	defaultView="overview"
	classes={{ childrenContainer: 'max-w-none' }}
	views={[
		{ label: m.infrastructure_tab_overview(), value: 'overview', content: overview },
		{ label: m.infrastructure_tab_mcp(), value: 'mcp', content: mcpServers },
		{ label: m.infrastructure_tab_claude(), value: 'claude', content: claude },
		{ label: m.infrastructure_tab_logs(), value: 'logs', content: logs }
	]}
	rightNavActions={navActions}
/>

{#snippet navActions(view: string)}
	{#if view !== 'logs'}
		<button class="btn btn-secondary btn-sm" onclick={load} disabled={loading}>
			{#if loading}
				<Loading class="size-4" />
			{:else}
				<RefreshCw class="size-4" />
			{/if}
			{m.infrastructure_refresh()}
		</button>
	{/if}
{/snippet}

{#snippet state(section?: InfraSection)}
	{#if error}
		<p class="text-error paper p-4 text-sm">{error}</p>
	{:else if !infra}
		<div class="flex h-48 items-center justify-center"><Loading class="size-6" /></div>
	{:else if !infra.kubernetes}
		<div class="paper p-4 text-sm">{infra.message}</div>
	{:else if section?.error}
		<p class="text-warning text-sm">{section.error}</p>
	{/if}
{/snippet}

{#snippet overview()}
	{@render state()}
	{#if infra?.kubernetes}
		{@const sections = [
			{ area: 'mcp', label: m.infrastructure_area_mcp(), c: counts(infra.mcpServers), view: 'mcp' },
			{
				area: 'claude',
				label: m.infrastructure_area_claude(),
				c: counts(infra.claude),
				view: 'claude'
			},
			{ area: 'obot', label: m.infrastructure_area_obot(), c: counts(infra.obot), view: 'overview' }
		]}
		<div class="flex flex-col gap-6">
			<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
				{#each sections as s (s.area)}
					<a
						class="paper hover:border-primary/50 flex flex-col gap-1 p-4 transition-colors"
						href={resolve(`/infrastructure?view=${s.view}`)}
					>
						<span class="text-muted-content text-xs">{s.label}</span>
						<span class="text-2xl font-semibold">{s.c.total}</span>
						<span class="text-xs">
							<span class="text-success"
								>{m.infrastructure_count_healthy({ count: s.c.healthy })}</span
							>
							{#if s.c.failing}
								· <span class="text-error"
									>{m.infrastructure_count_failing({ count: s.c.failing })}</span
								>
							{/if}
							{#if s.c.starting}
								· <span class="text-warning"
									>{m.infrastructure_count_starting({ count: s.c.starting })}</span
								>
							{/if}
						</span>
					</a>
				{/each}
			</div>

			<div class="flex flex-col gap-2">
				<h2 class="text-lg font-semibold">{m.infrastructure_problems()}</h2>
				{#if infra.problems.length === 0}
					<div class="paper flex items-center gap-2 p-4 text-sm">
						<CircleCheck class="text-success size-5" />
						{m.infrastructure_no_problems()}
					</div>
				{:else}
					<div class="flex flex-col gap-2">
						{#each infra.problems as p, i (i)}
							<div class="paper flex items-start gap-3 p-3">
								{#if p.severity === 'error'}
									<CircleAlert class="text-error mt-0.5 size-5 shrink-0" />
								{:else}
									<TriangleAlert class="text-warning mt-0.5 size-5 shrink-0" />
								{/if}
								<div class="flex min-w-0 flex-1 flex-col gap-0.5">
									<div class="flex flex-wrap items-center gap-2 text-sm">
										<span class="font-medium">{p.name || p.workload}</span>
										<span class="text-muted-content text-xs">{areaLabel(p.area)}</span>
										{#if p.workload && p.workload !== p.name}
											<span class="text-muted-content font-mono text-xs">{p.workload}</span>
										{/if}
									</div>
									<p class="text-sm break-words">{p.message}</p>
								</div>
								{#if p.link}
									<a
										class="link text-primary shrink-0 text-xs"
										href={resolve(p.link as `/${string}`)}>{m.infrastructure_open()}</a
									>
								{/if}
							</div>
						{/each}
					</div>
				{/if}
			</div>

			<McpServersSummary
				section={infra.mcpServers}
				onSelect={(name) => (selected = { area: 'mcp', name })}
			/>

			<ClaudeSummary section={infra.claude} />

			<div class="flex flex-col gap-2">
				<h2 class="text-lg font-semibold">{m.infrastructure_obot_server()}</h2>
				{#if infra.obot.error}
					<p class="text-warning text-sm">{infra.obot.error}</p>
				{/if}
				{#each infra.obot.workloads as w (w.name)}
					<WorkloadDetail workload={w} />
				{/each}
			</div>
		</div>
	{/if}
{/snippet}

{#snippet mcpServers()}
	{@render state(infra?.mcpServers)}
	{#if infra?.kubernetes}
		<div class="flex flex-col gap-4">
			<Table
				data={rows(infra.mcpServers)}
				fields={[
					'displayName',
					'health',
					'ready',
					'restarts',
					'userName',
					'requests',
					'limits',
					'created'
				]}
				headers={[
					{ title: m.infrastructure_col_name(), property: 'displayName' },
					{ title: m.infrastructure_col_health(), property: 'health' },
					{ title: m.infrastructure_col_ready(), property: 'ready' },
					{ title: m.infrastructure_col_restarts(), property: 'restarts' },
					{ title: m.infrastructure_col_user(), property: 'userName' },
					{ title: m.infrastructure_requests(), property: 'requests' },
					{ title: m.infrastructure_limits(), property: 'limits' },
					{ title: m.infrastructure_col_age(), property: 'created' }
				]}
				sortable={['displayName', 'health', 'restarts', 'userName', 'created']}
				filterable={['health', 'userName']}
				onClickRow={(d) => select('mcp', d.name)}
				setRowClasses={(d) =>
					selected?.area === 'mcp' && selected.name === d.name ? 'bg-primary/5' : ''}
				noDataMessage={m.infrastructure_no_mcp_workloads()}
				classes={{ root: 'shadow-none' }}
				pageSize={50}
			>
				{#snippet onRenderColumn(property, d)}
					{#if property === 'displayName'}
						<div class="flex flex-col">
							<span>{d.displayName || d.name}</span>
							<span class="text-muted-content text-xs">
								<span class="font-mono">{d.name}</span>
								{#if kindLabel(d)}· {kindLabel(d)}{/if}
							</span>
						</div>
					{:else if property === 'health'}
						<div class="flex flex-col gap-0.5">
							<HealthBadge health={d.health} />
							{#if d.warningEvents > 0}
								<span class="text-warning text-xs"
									>{m.infrastructure_warning_events({ count: d.warningEvents })}</span
								>
							{/if}
						</div>
					{:else if property === 'ready'}
						{d.readyReplicas}/{d.replicas}
					{:else if property === 'requests'}
						<span class="text-xs">{resources(d).req}</span>
					{:else if property === 'limits'}
						<span class="text-xs">{resources(d).lim}</span>
					{:else if property === 'created'}
						{formatTimeAgo(d.created).relativeTime}
					{:else}
						{d[property as keyof typeof d] ?? '—'}
					{/if}
				{/snippet}
			</Table>
			{#if selected?.area === 'mcp' && selectedWorkload}
				{#key selectedWorkload.name}
					<WorkloadDetail
						workload={selectedWorkload}
						showLogs
						onClose={() => (selected = undefined)}
					/>
				{/key}
			{/if}
		</div>
	{/if}
{/snippet}

{#snippet claude()}
	{@render state(infra?.claude)}
	{#if infra?.kubernetes}
		{#if !infra.claude.enabled}
			<div class="paper p-4 text-sm">{m.infrastructure_claude_disabled()}</div>
		{:else}
			{@const pods = infra.claude.workloads.flatMap((w) =>
				w.pods.map((p) => ({ ...p, id: p.name, pool: w }))
			)}
			<div class="flex flex-col gap-4">
				<Table
					data={rows(infra.claude)}
					fields={['displayName', 'health', 'ready', 'restarts', 'requests', 'created']}
					headers={[
						{ title: m.infrastructure_col_environment(), property: 'displayName' },
						{ title: m.infrastructure_col_health(), property: 'health' },
						{ title: m.infrastructure_col_workers(), property: 'ready' },
						{ title: m.infrastructure_col_restarts(), property: 'restarts' },
						{ title: m.infrastructure_col_worker_size(), property: 'requests' },
						{ title: m.infrastructure_col_age(), property: 'created' }
					]}
					onClickRow={(d) => select('claude', d.name)}
					noDataMessage={m.infrastructure_no_claude_pools()}
					classes={{ root: 'shadow-none' }}
				>
					{#snippet onRenderColumn(property, d)}
						{#if property === 'displayName'}
							<div class="flex flex-col">
								<span>{d.displayName || d.name}</span>
								<span class="text-muted-content font-mono text-xs">{d.name}</span>
							</div>
						{:else if property === 'health'}
							<HealthBadge health={d.health} />
						{:else if property === 'ready'}
							{d.readyReplicas}/{d.replicas}
						{:else if property === 'requests'}
							<span class="text-xs">{resources(d).lim}</span>
						{:else if property === 'created'}
							{formatTimeAgo(d.created).relativeTime}
						{:else}
							{d[property as keyof typeof d] ?? '—'}
						{/if}
					{/snippet}
				</Table>

				<h2 class="text-lg font-semibold">{m.infrastructure_worker_pods()}</h2>
				<Table
					data={pods}
					fields={['name', 'environment', 'status', 'ready', 'restarts', 'sessions', 'created']}
					headers={[
						{ title: m.infrastructure_col_pod(), property: 'name' },
						{ title: m.infrastructure_col_environment(), property: 'environment' },
						{ title: m.infrastructure_col_status(), property: 'status' },
						{ title: m.infrastructure_col_ready(), property: 'ready' },
						{ title: m.infrastructure_col_restarts(), property: 'restarts' },
						{ title: m.infrastructure_col_sessions(), property: 'sessions' },
						{ title: m.infrastructure_col_age(), property: 'created' }
					]}
					noDataMessage={m.infrastructure_no_pods()}
					classes={{ root: 'shadow-none' }}
				>
					{#snippet onRenderColumn(property, d)}
						{#if property === 'name'}
							<span class="font-mono text-xs">{d.name}</span>
						{:else if property === 'environment'}
							{d.pool.displayName || d.pool.name}
						{:else if property === 'status'}
							<span class={d.status === 'Running' ? '' : 'text-warning'}>{d.status}</span>
						{:else if property === 'sessions'}
							{#if d.sessions?.length}
								<div class="flex flex-col gap-0.5">
									{#each d.sessions as s (s.id)}
										<span class="text-xs">
											<span class="font-medium">{s.name || s.id}</span>
											<span class="text-muted-content">
												· {s.userName || s.userID}{#if s.state}
													· {s.state}{/if}</span
											>
										</span>
									{/each}
								</div>
							{:else}
								<span class="text-muted-content text-xs">{m.infrastructure_idle()}</span>
							{/if}
						{:else if property === 'created'}
							{formatTimeAgo(d.created).relativeTime}
						{:else}
							{d[property as keyof typeof d] ?? '—'}
						{/if}
					{/snippet}
				</Table>
				{#if selected?.area === 'claude' && selectedWorkload}
					{#key selectedWorkload.name}
						<WorkloadDetail workload={selectedWorkload} onClose={() => (selected = undefined)} />
					{/key}
				{/if}
			</div>
		{/if}
	{/if}
{/snippet}

{#snippet logs()}
	<ObotLogs />
{/snippet}
