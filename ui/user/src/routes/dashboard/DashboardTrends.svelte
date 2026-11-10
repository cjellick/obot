<script lang="ts">
	// The Dashboard's 30-day charts, kept from the original Dashboard below the hub's area cards.
	import Skeleton from '$lib/components/Skeleton.svelte';
	import TweenedMetric from '$lib/components/TweenedMetric.svelte';
	import TokenUsageTimelineCard from '$lib/components/admin/token-usage/TokenUsageTimelineCard.svelte';
	import DonutGraph from '$lib/components/graph/DonutGraph.svelte';
	import GraphTitle from '$lib/components/graph/GraphTitle.svelte';
	import HorizontalBarList from '$lib/components/graph/HorizontalBarList.svelte';
	import { DEFAULT_MCP_CATALOG_ID } from '$lib/constants';
	import { formatNumber } from '$lib/format';
	import { m } from '$lib/i18n';
	import { stripMarkdownToText } from '$lib/markdown';
	import {
		AdminService,
		UserService,
		type MCPCatalogEntry,
		type MCPCatalogServer
	} from '$lib/services';
	import { entryTypeDonutLegend } from '$lib/services/dashboard/constants';
	import type {
		AvgToolCallResponseTimeRow,
		TopServerUsageRow,
		TopToolCallRow
	} from '$lib/services/dashboard/types';
	import {
		avgToolCallResponseTimeFromStats,
		compileServerAndEntries,
		deploymentStatusGridColClass,
		deploymentStatusGridShowBorderRight,
		topServersFromStats,
		topToolCallsFromStats
	} from '$lib/services/dashboard/utils';
	import { getMCPDisplayName } from '$lib/services/user/mcp';
	import { mcpServersAndEntries, profile, version } from '$lib/stores';
	import { resolve } from '$lib/url';
	import { ChevronRight, Server, Siren, Wrench } from '@lucide/svelte';
	import { subMonths } from 'date-fns';
	import { onMount } from 'svelte';
	import { fade } from 'svelte/transition';
	import { twMerge } from 'tailwind-merge';

	const isAdminView = $derived(!!profile.current.hasAdminAccess?.());
	let loading = $state(true);
	let loadingToolUsage = $state(true);

	const doesSupportK8sUpdates = $derived(version.current.engine === 'kubernetes');

	let topToolCalls = $state<TopToolCallRow[]>([]);
	let topServerUsage = $state<TopServerUsageRow[]>([]);
	let avgToolCallResponseTime = $state<AvgToolCallResponseTimeRow[]>([]);
	const maxToolsToShow = 5;
	const maxServersToShow = 10;

	const end = new Date();
	const start = subMonths(end, 1);

	let deployedCatalogEntryServers = $state<MCPCatalogServer[]>([]);
	let deployedWorkspaceCatalogEntryServers = $state<MCPCatalogServer[]>([]);
	let serversData = $derived.by(() => {
		if (mcpServersAndEntries.current.loading || loading) return [];
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const seen = new Set<string>();
		const result: MCPCatalogServer[] = [];
		for (const list of [
			deployedCatalogEntryServers,
			deployedWorkspaceCatalogEntryServers,
			mcpServersAndEntries.current.servers
		]) {
			for (const server of list) {
				if (server.deleted || seen.has(server.id)) continue;
				seen.add(server.id);
				result.push(server);
			}
		}
		return result;
	});

	const serverAndEntries = $derived(mcpServersAndEntries.current);
	const { graphData, popularServers, totalServers, deploymentStatusBreakdown } = $derived(
		compileServerAndEntries(serversData, serverAndEntries.entries, doesSupportK8sUpdates)
	);

	let isBootStrapUser = $derived(profile.current.isBootstrapUser?.() ?? false);

	onMount(async () => {
		UserService.listMcpAuditLogUsageStats(
			{ start_time: start.toISOString(), end_time: end.toISOString() },
			{ dontLogErrors: true }
		)
			.then((stats) => {
				const statsToUse = (stats.items ?? []).filter(
					(s) =>
						!s.mcpID.startsWith('sms1') &&
						!s.mcpServerDisplayName.startsWith('nba1') &&
						!s.mcpServerDisplayName.startsWith('Obot ')
				);
				const adjustedStats = { ...stats, items: statsToUse };
				topToolCalls = topToolCallsFromStats(adjustedStats);
				topServerUsage = topServersFromStats(adjustedStats);
				avgToolCallResponseTime = avgToolCallResponseTimeFromStats(adjustedStats);
			})
			.catch(() => {
				// The charts show their empty states.
			})
			.finally(() => {
				loadingToolUsage = false;
			});

		if (profile.current.hasAdminAccess?.()) {
			const [catalogServers, workspaceServers] = await Promise.all([
				AdminService.listAllCatalogDeployedSingleRemoteServers(DEFAULT_MCP_CATALOG_ID).catch(
					() => []
				),
				AdminService.listAllWorkspaceDeployedSingleRemoteServers().catch(() => [])
			]);
			deployedCatalogEntryServers = catalogServers;
			deployedWorkspaceCatalogEntryServers = workspaceServers;
		}
		loading = false;
	});

	const deploymentStatusLabels: Record<string, () => string> = {
		Available: m.dashboard_status_available,
		Progressing: m.dashboard_status_progressing,
		Unavailable: m.dashboard_status_unavailable,
		'Needs Attention': m.dashboard_status_needs_attention,
		Shutdown: m.dashboard_status_shutdown,
		Unknown: m.core_unknown,
		Pending: m.core_status_pending
	};

	function deploymentStatusLabel(status: string) {
		return deploymentStatusLabels[status]?.() ?? status;
	}

	function getServerUrl(server: MCPCatalogServer) {
		return `/mcp-servers/s/${server.id}?view=server-instances`;
	}

	function getEntryUrl(entry: MCPCatalogEntry) {
		return `/mcp-servers/c/${entry.id}?view=server-instances`;
	}

	const graphContainerClasses = 'px-6 py-4 flex flex-col gap-4 h-full';
</script>

<div class="@container flex min-w-0 w-full flex-col gap-4">
	{#if isAdminView}
		<TokenUsageTimelineCard startDate={start} endDate={end} />
		<div class="grid grid-cols-1 items-stretch gap-4 @3xl:grid-cols-2">
			{@render toolUsageGraph()}
			{@render serverActivityGraph()}
		</div>
		<div class="grid grid-cols-1 items-stretch gap-4 @3xl:grid-cols-3">
			{@render popularTools()}
			{@render toolAverageResponseTime()}
			{@render topServerDeploymentList()}
		</div>
	{:else}
		{@render toolUsageGraph()}
		<div class="grid grid-cols-1 items-stretch gap-4 @3xl:grid-cols-2">
			{@render popularTools()}
			{@render toolAverageResponseTime()}
		</div>
	{/if}
</div>

{#snippet toolUsageGraph()}
	{#if loadingToolUsage}
		<Skeleton type="card" class="min-h-72 h-full w-full" />
	{:else}
		<div in:fade={{ duration: 150 }} class="paper h-full min-h-72 gap-0 w-full p-0">
			<GraphTitle
				title={m.dashboard_top_servers_used()}
				description={m.dashboard_last_30_days()}
				element="h4"
			/>
			<div class="p-4">
				<HorizontalBarList
					data={topServerUsage.slice(0, maxServersToShow)}
					labelKey="serverName"
					valueKey="count"
					formatValue={(value) => Math.round(value).toString()}
				>
					{#snippet tooltipContent(item)}
						<div class="flex flex-col gap-0 text-xs">
							<div class="text-muted-content text-xs">{item.label}</div>
						</div>
						<div class="text-base-content font-semibold">
							{m.dashboard_calls_count({ count: item.value })}
						</div>
					{/snippet}
				</HorizontalBarList>
			</div>
		</div>
	{/if}
{/snippet}

{#snippet serverActivityGraph()}
	{#if serverAndEntries.loading || loading}
		<Skeleton type="card" class="min-h-96 h-full w-full" />
	{:else}
		<div in:fade={{ duration: 150 }} class="paper h-full min-h-96 p-0 gap-0">
			<GraphTitle title={m.dashboard_server_activity()} element="h4" />
			<div class="px-6 py-4 flex flex-col gap-4">
				{#if doesSupportK8sUpdates && deploymentStatusBreakdown.length > 0}
					<div class="mb-2 grid grid-cols-12 gap-x-2 gap-y-5">
						{#each deploymentStatusBreakdown as row, i (row.status)}
							<div
								class={twMerge(
									'flex flex-col items-center justify-center px-1 text-center',
									deploymentStatusGridColClass(i, deploymentStatusBreakdown.length),
									deploymentStatusGridShowBorderRight(i, deploymentStatusBreakdown.length) &&
										'border-r border-base-300'
								)}
							>
								<div class="flex items-center gap-1">
									<div class="font-semibold text-3xl">
										<TweenedMetric target={row.count} />
									</div>
									{#if row.status === 'Available'}
										<Server class="size-6 text-primary" />
									{:else if row.status === 'Needs Attention'}
										<Siren class="size-6 text-warning" />
									{:else}
										<Server class="size-6 text-muted-content/75" />
									{/if}
								</div>
								<div class="text-xs">{deploymentStatusLabel(row.status)}</div>
							</div>
						{/each}
					</div>
				{:else}
					<div class="mb-2 flex flex-col justify-center items-center">
						<div class="flex w-full gap-2 items-center justify-center">
							<div class="font-semibold text-3xl">
								<TweenedMetric target={totalServers} />
							</div>
							<Server class="size-6 text-primary" />
						</div>
						<div class="text-xs">{m.dashboard_total_currently_active()}</div>
					</div>
				{/if}

				<div class="flex flex-col items-center justify-center grow h-80">
					{#if graphData.some((g) => g.value > 0)}
						<DonutGraph
							class="h-80"
							donutRatio={0.65}
							data={graphData}
							legend={doesSupportK8sUpdates ? entryTypeDonutLegend : undefined}
						/>
					{:else}
						<p class="font-light text-xs text-muted-content pt-2 text-center">
							{m.dashboard_no_servers_deployed()}
						</p>
					{/if}
				</div>

				{#if !isBootStrapUser && totalServers > 0}
					<div class="flex justify-end">
						<a
							href={resolve('/mcp-servers?view=deployments')}
							class="text-[11px] transition-colors self-end translate-x-2 duration-200 bg-base-400/50 hover:bg-base-400 rounded-md py-0.5 w-fit px-2 flex items-center gap-1"
						>
							{m.dashboard_see_more()}
							<ChevronRight class="size-3" />
						</a>
					</div>
				{/if}
			</div>
		</div>
	{/if}
{/snippet}

{#snippet topServerDeploymentList()}
	<div in:fade={{ duration: 150 }} class="paper h-full gap-0 p-0">
		<GraphTitle title={m.dashboard_most_deployed_servers()} element="h4" />
		<div class={graphContainerClasses}>
			{#if mcpServersAndEntries.current.loading || loading}
				<Skeleton type="list" />
			{:else if popularServers.length > 0}
				<div class="flex flex-col gap-2 -ml-2 w-[calc(100%+1rem)]">
					{#each popularServers as info (info.id)}
						{@const icon =
							'server' in info ? info.server?.manifest.icon : info.entry?.manifest.icon}
						{@const displayName =
							'server' in info ? getMCPDisplayName(info.server) : info.entry?.manifest.name}
						{@const description =
							'server' in info
								? info.server?.manifest.description
								: info.entry?.manifest.description}
						{@const url = info.server
							? getServerUrl(info.server)
							: info.entry
								? getEntryUrl(info.entry)
								: undefined}
						<a
							class="flex gap-2 w-full items-center dark:hover:bg-base-300 hover:bg-base-200 transition-colors duration-150 px-2 py-1 rounded-md"
							href={url ? resolve(url as `/${string}`) : undefined}
						>
							{#if icon}
								<img
									src={icon}
									alt={info.id}
									class="size-9 bg-base-200 dark:bg-base-300 rounded-md p-1"
								/>
							{:else}
								<Server class="size-9 opacity-65 bg-base-200 rounded-md p-1" />
							{/if}
							<div class="flex flex-col gap-0.5 max-w-[calc(100%-4.5rem)] grow">
								<p class="text-sm font-medium">{displayName}</p>
								{#if description}
									<p class="text-xs truncate line-clamp-1 break-all font-light">
										{stripMarkdownToText(description ?? '')}
									</p>
								{/if}
								<p class="text-xs text-muted-content italic">
									{m.dashboard_deployed_times({ count: info.count })}
								</p>
							</div>
							<ChevronRight class="size-5 shrink-0" />
						</a>
					{/each}
				</div>
			{:else}
				<p
					class="text-xs text-muted-content pt-2 font-light text-center h-full flex items-center justify-center grow"
				>
					{m.dashboard_no_servers_deployed()}
				</p>
			{/if}
			<div class="flex grow"></div>
			{#if popularServers.length > 0 && !isBootStrapUser}
				<a
					href={resolve('/mcp-servers')}
					class="justify-end self-end text-[11px] translate-x-2 transition-colors duration-200 bg-base-400/50 hover:bg-base-400 rounded-md py-0.5 w-fit px-2 flex items-center gap-1"
				>
					{m.dashboard_see_more()}
					<ChevronRight class="size-3" />
				</a>
			{/if}
		</div>
	</div>
{/snippet}

{#snippet popularTools()}
	<div class="paper h-full min-h-72 flex flex-col p-0 gap-0">
		<GraphTitle
			title={m.dashboard_recently_popular_tools()}
			description={m.dashboard_last_30_days()}
			element="h4"
		/>
		<div class={graphContainerClasses}>
			{#if loadingToolUsage}
				<Skeleton type="list" class="w-full" count={maxToolsToShow} />
			{:else if topToolCalls.length === 0}
				<p
					class="text-xs text-muted-content pt-2 font-light grow flex items-center justify-center h-full text-center"
				>
					{m.dashboard_no_recent_tool_calls()}
				</p>
			{:else}
				<ul class="flex flex-col gap-2">
					{#each topToolCalls.slice(0, maxToolsToShow) as row (row.compositeKey)}
						<li class="flex gap-2 items-center">
							<div
								class="size-8 items-center justify-center shrink-0 bg-base-200 dark:bg-base-300 rounded-md p-1"
							>
								<Wrench class="size-6 opacity-65 shrink-0" />
							</div>
							<div class="flex flex-col gap-1 min-w-0">
								<p class="text-sm font-medium truncate">
									{row.toolLabel.split('.').slice(1).join('.') || row.compositeKey}
								</p>
								<p class="text-xs text-muted-content">
									{m.dashboard_calls_with_server({
										count: formatNumber(row.count),
										server: row.serverDisplayName
									})}
								</p>
							</div>
						</li>
					{/each}
				</ul>
			{/if}
			<div class="flex grow min-h-0"></div>
			{#if topToolCalls.length > 0 && !isBootStrapUser}
				<a
					href={resolve('/usage')}
					class="text-[11px] translate-x-2 self-end bg-base-400/50 transition-colors duration-200 hover:bg-base-400 rounded-md py-0.5 w-fit px-2 flex items-center gap-1 mt-2"
				>
					{m.dashboard_see_more()}
					<ChevronRight class="size-3" />
				</a>
			{/if}
		</div>
	</div>
{/snippet}

{#snippet toolAverageResponseTime()}
	<div class="paper h-full min-h-72 gap-0 flex flex-col p-0">
		<GraphTitle
			title={m.dashboard_tool_call_avg_response_time()}
			description={m.dashboard_last_30_days()}
			element="h4"
		/>
		<div class={graphContainerClasses}>
			{#if loadingToolUsage}
				<Skeleton type="list" class="w-full" count={maxToolsToShow} />
			{:else if avgToolCallResponseTime.length === 0}
				<p
					class="text-xs text-muted-content pt-2 font-light grow flex items-center justify-center h-full text-center"
				>
					{m.dashboard_no_recent_tool_calls()}
				</p>
			{:else}
				<div class="flex flex-col gap-4 w-full">
					<ul class="flex flex-col gap-2">
						{#each avgToolCallResponseTime.slice(0, maxToolsToShow) as row (row.toolName)}
							<li class="flex gap-2 items-center">
								<div class="flex flex-col gap-1 min-w-0 grow pr-4">
									<p class="text-sm font-medium truncate">
										{row.toolName.split('.').slice(1).join('.')}
									</p>
									<p class="text-xs text-muted-content">
										{row.serverDisplayName}
									</p>
								</div>
								<div class="text-sm">
									{row.averageResponseTimeMs.toFixed(2)}ms
								</div>
							</li>
						{/each}
					</ul>
				</div>
			{/if}
			<div class="flex grow min-h-0"></div>
			{#if avgToolCallResponseTime.length > 0 && !isBootStrapUser}
				<a
					href={resolve('/usage')}
					class="text-[11px] translate-x-2 self-end bg-base-400/50 transition-colors duration-200 hover:bg-base-400 rounded-md py-0.5 w-fit px-2 flex items-center gap-1 mt-2"
				>
					{m.dashboard_see_more()}
					<ChevronRight class="size-3" />
				</a>
			{/if}
		</div>
	</div>
{/snippet}
