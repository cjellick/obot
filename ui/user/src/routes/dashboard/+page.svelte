<script lang="ts">
	import Layout from '$lib/components/Layout.svelte';
	import { formatTokenUsageUSD } from '$lib/components/admin/token-usage/tokenUsageTimeline';
	import { formatNumber } from '$lib/format';
	import { m } from '$lib/i18n';
	import { profile, version } from '$lib/stores';
	import { resolve } from '$lib/url';
	import DashboardTrends from './DashboardTrends.svelte';
	import HubCard from './HubCard.svelte';
	import HubStat from './HubStat.svelte';
	import SegmentBar from './SegmentBar.svelte';
	import Sparkline from './Sparkline.svelte';
	import { HubLoad, combinedStatus, type LoadStatus } from './hub.svelte';
	import {
		HIGH_RISK,
		loadBriefing,
		loadClaude,
		loadDevices,
		loadInfrastructure,
		loadLLM,
		loadMCPInventory,
		loadMCPUsage,
		loadServerRequests,
		loadSkills,
		loadSupervisor,
		loadUsers
	} from './loaders';
	import {
		Boxes,
		BrainCircuit,
		Laptop,
		PencilRuler,
		Plug,
		Server,
		Settings2,
		ShieldCheck,
		Sparkles,
		SquareTerminal,
		Users
	} from '@lucide/svelte';
	import { onMount } from 'svelte';

	// The Dashboard is the hub: a headline band for the last 7 days, one card per area of Obot
	// with its live numbers and anything needing attention, then the 30-day charts. Every card
	// loads on its own; cards for areas the viewer can't use stay hidden.

	const hasAdminAccess = !!profile.current.hasAdminAccess?.();
	const isAdmin = !!profile.current.isAdmin?.();
	const claudeAvailable =
		version.current.engine === 'docker' ||
		(version.current.engine === 'kubernetes' && !!version.current.claudeEnvironmentsEnabled);

	const supervisor = new HubLoad(loadSupervisor, hasAdminAccess);
	const briefing = new HubLoad(loadBriefing, hasAdminAccess);
	const requests = new HubLoad(loadServerRequests);
	const llm = new HubLoad(loadLLM, hasAdminAccess);
	const mcpUsage = new HubLoad(loadMCPUsage);
	const mcpInventory = new HubLoad(() => loadMCPInventory(hasAdminAccess));
	const skills = new HubLoad(() => loadSkills(hasAdminAccess));
	const claude = new HubLoad(() => loadClaude(hasAdminAccess), claudeAvailable);
	const infra = new HubLoad(loadInfrastructure, isAdmin);
	const devices = new HubLoad(loadDevices, hasAdminAccess);
	const users = new HubLoad(loadUsers);

	onMount(() => {
		for (const l of [
			supervisor,
			briefing,
			requests,
			llm,
			mcpUsage,
			mcpInventory,
			skills,
			claude,
			infra,
			devices,
			users
		]) {
			void l.load();
		}
	});

	const briefingHref = $derived(
		`/ai?ask=${encodeURIComponent(m.dashboard_hub_briefing_question())}`
	);

	const percent = (n: number) => `${Math.round(n)}%`;

	interface Headline {
		id: string;
		label: string;
		status: LoadStatus;
		value?: number;
		format?: (n: number) => string;
		hint?: string;
		hintTone?: 'error';
		href: string;
	}

	const headlines = $derived<Headline[]>(
		[
			{
				id: 'spend',
				label: m.dashboard_hub_llm_spend(),
				status: llm.status,
				value: llm.data?.spend,
				format: formatTokenUsageUSD,
				href: '/usage?view=llm'
			},
			{
				id: 'requests',
				label: m.dashboard_hub_llm_requests(),
				status: llm.status,
				value: llm.data?.requests,
				format: (n: number) => formatNumber(Math.round(n)),
				href: '/usage?view=llm'
			},
			{
				id: 'active-users',
				label: m.dashboard_hub_active_users(),
				status: users.status,
				value: users.data?.active,
				hint: users.data ? m.dashboard_hub_of_total_users({ count: users.data.users }) : undefined,
				href: '/identity-access?view=users'
			},
			{
				id: 'tool-calls',
				label: m.dashboard_hub_mcp_tool_calls(),
				status: mcpUsage.status,
				value: mcpUsage.data?.toolCalls,
				format: (n: number) => formatNumber(Math.round(n)),
				href: '/usage?view=mcp'
			},
			{
				id: 'sessions',
				label: m.dashboard_hub_agent_sessions(),
				status: supervisor.status,
				value: supervisor.data?.sessions,
				hint:
					supervisor.data?.highRisk !== undefined
						? m.dashboard_hub_high_risk_count({ count: supervisor.data.highRisk })
						: undefined,
				hintTone: supervisor.data?.highRisk ? 'error' : undefined,
				href: '/sessions?range=7d'
			}
		].filter((h) => h.status !== 'off' && h.status !== 'hidden') as Headline[]
	);

	// Attention lines, worst first.

	const supervisorAttention = $derived.by(() => {
		const d = supervisor.data;
		if (!d) return undefined;
		if (d.pendingApprovals) {
			return {
				text: m.dashboard_hub_approvals_waiting({ count: d.pendingApprovals }),
				href: '/approvals',
				tone: 'error' as const
			};
		}
		if (d.highRisk) {
			return {
				text: m.dashboard_hub_high_risk_sessions_week({ count: d.highRisk }),
				href: `/sessions?range=7d&min_risk=${HIGH_RISK}`,
				tone: 'warning' as const
			};
		}
		if (d.shadowed) {
			return {
				text: m.dashboard_hub_shadow_hits_review({ count: d.shadowed }),
				href: '/policies',
				tone: 'warning' as const
			};
		}
		return undefined;
	});

	const requestsAttention = $derived(
		requests.data?.pending
			? {
					text: m.dashboard_hub_requests_waiting({ count: requests.data.pending }),
					href: '/mcp-server-requests',
					tone: 'warning' as const
				}
			: undefined
	);

	const llmAttention = $derived(
		llm.data?.successRate !== undefined && llm.data.successRate < 0.95
			? {
					text: m.dashboard_hub_llm_failures({
						percent: percent((1 - llm.data.successRate) * 100)
					}),
					href: '/audit-logs?view=llm',
					tone: 'warning' as const
				}
			: undefined
	);

	const skillsAttention = $derived(
		skills.data?.invalid
			? {
					text: m.dashboard_hub_skills_invalid({ count: skills.data.invalid }),
					href: '/skills',
					tone: 'warning' as const
				}
			: undefined
	);

	const claudeAttention = $derived(
		claude.data?.blockedEgress
			? {
					text: m.dashboard_hub_egress_blocked({ count: claude.data.blockedEgress }),
					href: '/audit-logs?view=egress&outcome=denied',
					tone: 'warning' as const
				}
			: undefined
	);

	const infraAttention = $derived(
		infra.data?.firstProblem
			? {
					text: `${infra.data.firstProblem.name}: ${infra.data.firstProblem.message}`,
					href: '/infrastructure',
					tone: (infra.data.errors > 0 ? 'error' : 'warning') as 'error' | 'warning'
				}
			: undefined
	);

	const platformAttention = $derived.by(() => {
		const v = version.current;
		if ((v.licenseEntitlementViolations?.length ?? 0) > 0) {
			return {
				text: m.dashboard_hub_license_issue(),
				href: '/admin/platform?view=license',
				tone: 'error' as const
			};
		}
		if (v.upgradeAvailable && v.latestVersion) {
			return {
				text: m.dashboard_hub_upgrade_available({ version: v.latestVersion }),
				href: '/admin/platform',
				tone: 'warning' as const
			};
		}
		return undefined;
	});

	const suggestions = $derived([
		m.dashboard_hub_ai_suggestion_risk(),
		m.dashboard_hub_ai_suggestion_spend(),
		m.dashboard_hub_ai_suggestion_new_servers()
	]);
</script>

<svelte:head>
	<title>{m.dashboard_page_title()}</title>
</svelte:head>

<Layout title={m.nav_dashboard()} classes={{ childrenContainer: 'max-w-none', container: '' }}>
	<div class="@container flex min-w-0 w-full flex-col gap-6 pb-8">
		<!-- Headline band: the last 7 days at a glance -->
		<section class="paper gap-0 p-0" aria-labelledby="hub-headline-title">
			<div
				class="flex flex-wrap items-center justify-between gap-3 border-b border-base-300 px-5 py-3"
			>
				<div>
					<h2 id="hub-headline-title" class="text-sm font-semibold">
						{m.dashboard_hub_headline_title()}
					</h2>
					<p class="text-xs text-muted-content">{m.dashboard_hub_headline_description()}</p>
				</div>
				<a href={resolve(briefingHref)} class="btn btn-primary flex items-center gap-2 text-sm">
					<Sparkles class="size-4" />
					{m.dashboard_hub_ask_briefing()}
				</a>
			</div>
			<div class="grid grid-cols-2 @2xl:grid-cols-3 @5xl:grid-cols-5">
				{#each headlines as h (h.id)}
					<a
						href={resolve(h.href)}
						class="group flex flex-col gap-1 border-base-300 px-5 py-4 transition-colors hover:bg-base-200 dark:hover:bg-base-300/60"
						aria-label={h.label}
					>
						<span class="text-xs text-muted-content">{h.label}</span>
						{#if h.status === 'loading'}
							<span
								class="skeleton h-7 w-20 rounded"
								role="status"
								aria-label={m.dashboard_hub_loading()}
							></span>
						{:else if h.status === 'error' || h.value === undefined}
							<span class="text-2xl font-semibold text-muted-content">—</span>
						{:else}
							<span class="text-2xl font-semibold tabular-nums tracking-tight">
								{#if h.format}
									{h.format(h.value)}
								{:else}
									{h.value.toLocaleString()}
								{/if}
							</span>
						{/if}
						{#if h.hint}
							<span
								class={[
									'text-[11px]',
									h.hintTone === 'error' ? 'text-error' : 'text-muted-content'
								]}
							>
								{h.hint}
							</span>
						{/if}
					</a>
				{/each}
			</div>
		</section>

		<!-- One card per area -->
		<section aria-labelledby="hub-areas-title" class="flex flex-col gap-3">
			<h2 id="hub-areas-title" class="text-sm font-semibold">{m.dashboard_hub_areas_title()}</h2>
			<div class="grid grid-cols-1 gap-4 @2xl:grid-cols-2 @5xl:grid-cols-3">
				<HubCard
					id="supervisor"
					title={m.nav_supervision()}
					description={m.dashboard_hub_supervisor_description()}
					icon={ShieldCheck}
					href="/supervision"
					status={supervisor.status}
					attention={supervisorAttention}
					class="@5xl:col-span-2"
					links={[
						{ label: m.nav_supervision_overview(), href: '/supervision' },
						{ label: m.nav_activity(), href: '/sessions' },
						{ label: m.nav_approvals(), href: '/approvals' },
						{ label: m.nav_rules(), href: '/tool-labels' }
					]}
				>
					{#if supervisor.data}
						{@const d = supervisor.data}
						<div class="grid grid-cols-2 gap-3 @xl:grid-cols-4">
							<HubStat
								label={m.dashboard_hub_pending_approvals()}
								value={d.pendingApprovals}
								tone="error"
							/>
							<HubStat
								label={m.dashboard_hub_high_risk_sessions()}
								value={d.highRisk}
								tone="warning"
							/>
							<HubStat label={m.dashboard_hub_shadow_hits()} value={d.shadowed} />
							<HubStat label={m.dashboard_hub_denied()} value={d.denied} />
						</div>
						<SegmentBar
							segments={[
								{ label: m.dashboard_hub_allowed(), value: d.allowed, color: 'bg-success' },
								{ label: m.dashboard_hub_denied(), value: d.denied, color: 'bg-error' },
								{ label: m.dashboard_hub_shadowed(), value: d.shadowed, color: 'bg-warning' }
							]}
						/>
						{#if briefing.data}
							<p class="line-clamp-2 text-xs italic text-muted-content">
								<Sparkles class="mr-1 inline size-3.5 text-primary" />{briefing.data}
							</p>
						{/if}
					{/if}
				</HubCard>

				<HubCard
					id="connect"
					title={m.nav_server_requests()}
					description={m.dashboard_hub_connect_description()}
					icon={Plug}
					href="/mcp-server-requests"
					status={requests.status}
					attention={requestsAttention}
					links={[{ label: m.dashboard_hub_link_connect(), href: '/mcp-server-requests' }]}
				>
					{#if requests.data}
						<div class="grid grid-cols-3 gap-3">
							<HubStat
								label={m.dashboard_hub_pending_requests()}
								value={requests.data.pending}
								tone="warning"
							/>
							<HubStat label={m.dashboard_hub_approved_requests()} value={requests.data.approved} />
							<HubStat label={m.dashboard_hub_total_requests()} value={requests.data.total} />
						</div>
					{/if}
				</HubCard>

				<HubCard
					id="llm"
					title={m.nav_llm_gateway()}
					description={m.dashboard_hub_llm_description()}
					icon={BrainCircuit}
					href="/models"
					status={llm.status}
					attention={llmAttention}
					links={[
						{ label: m.nav_llm_gateway(), href: '/models' },
						{ label: m.nav_usage(), href: '/usage?view=llm' },
						{ label: m.nav_audit_logs(), href: '/audit-logs?view=llm' }
					]}
				>
					{#if llm.data}
						{@const d = llm.data}
						<div class="grid grid-cols-3 gap-3">
							<HubStat
								label={m.dashboard_hub_spend()}
								value={d.spend}
								format={formatTokenUsageUSD}
							/>
							<HubStat
								label={m.dashboard_hub_requests()}
								value={d.requests}
								format={(n) => formatNumber(Math.round(n))}
							/>
							<HubStat
								label={m.dashboard_hub_success_rate()}
								value={d.successRate === undefined ? undefined : d.successRate * 100}
								format={percent}
							/>
						</div>
						<Sparkline values={d.buckets} label={m.dashboard_hub_requests_over_time()} />
						{#if d.topModel}
							<p class="truncate text-xs text-muted-content">
								{m.dashboard_hub_top_model({
									model: d.topModel.model,
									spend: formatTokenUsageUSD(d.topModel.spend)
								})}
							</p>
						{/if}
					{/if}
				</HubCard>

				<HubCard
					id="mcp"
					title={m.dashboard_hub_mcp_title()}
					description={m.dashboard_hub_mcp_description()}
					icon={Server}
					href="/mcp-servers"
					status={combinedStatus(mcpInventory, mcpUsage)}
					links={[
						{ label: m.nav_vmcps(), href: '/vmcps' },
						{ label: m.nav_mcp_servers(), href: '/mcp-servers' },
						{ label: m.nav_usage(), href: '/usage?view=mcp' },
						{ label: m.nav_audit_logs(), href: '/audit-logs' }
					]}
				>
					<div class="grid grid-cols-3 gap-3">
						<HubStat label={m.nav_vmcps()} value={mcpInventory.data?.vmcps} />
						<HubStat
							label={m.dashboard_hub_catalog_entries()}
							value={mcpInventory.data?.catalogEntries}
						/>
						<HubStat
							label={m.dashboard_hub_tool_calls()}
							value={mcpUsage.data?.toolCalls}
							format={(n) => formatNumber(Math.round(n))}
						/>
					</div>
					{#if mcpUsage.data?.topServer}
						<p class="truncate text-xs text-muted-content">
							{m.dashboard_hub_top_server({
								server: mcpUsage.data.topServer.name,
								count: formatNumber(mcpUsage.data.topServer.count)
							})}
						</p>
					{/if}
				</HubCard>

				<HubCard
					id="devices"
					title={m.nav_inventory()}
					description={m.dashboard_hub_devices_description()}
					icon={Laptop}
					href="/inventory?view=overview"
					status={devices.status}
					links={[
						{ label: m.dashboard_unique_devices(), href: '/inventory?view=devices' },
						{ label: m.dashboard_device_clients(), href: '/inventory?view=device-clients' },
						{ label: m.dashboard_unique_mcps(), href: '/inventory?view=device-mcp-servers' },
						{ label: m.dashboard_unique_skills(), href: '/inventory?view=device-skills' }
					]}
				>
					{#if devices.data}
						{@const d = devices.data}
						<div class="grid grid-cols-3 gap-3">
							<HubStat label={m.dashboard_hub_scanned_devices()} value={d.devices} />
							<HubStat label={m.dashboard_unique_clients()} value={d.clients} />
							<HubStat label={m.dashboard_unique_mcps()} value={d.mcpServers} />
						</div>
						{#if d.topClient}
							<p class="truncate text-xs text-muted-content">
								{m.dashboard_hub_top_client({
									client: d.topClient.name,
									count: d.topClient.devices
								})}
							</p>
						{/if}
					{/if}
				</HubCard>

				<HubCard
					id="claude"
					title={m.nav_claude_environments()}
					description={m.dashboard_hub_claude_description()}
					icon={SquareTerminal}
					href="/claude-environments"
					status={claude.status}
					attention={claudeAttention}
					links={[
						{ label: m.nav_claude_environments(), href: '/claude-environments' },
						{ label: m.dashboard_hub_sessions(), href: '/claude-environments?view=sessions' },
						...(hasAdminAccess
							? [{ label: m.dashboard_hub_egress_logs(), href: '/audit-logs?view=egress' }]
							: [])
					]}
				>
					{#if claude.data}
						<div class="grid grid-cols-3 gap-3">
							<HubStat label={m.dashboard_hub_environments()} value={claude.data.environments} />
							<HubStat label={m.dashboard_hub_running_sessions()} value={claude.data.running} />
							{#if hasAdminAccess}
								<HubStat
									label={m.dashboard_hub_blocked_egress()}
									value={claude.data.blockedEgress}
									tone="warning"
								/>
							{:else}
								<HubStat label={m.dashboard_hub_sessions()} value={claude.data.sessions} />
							{/if}
						</div>
					{/if}
				</HubCard>

				<HubCard
					id="infrastructure"
					title={m.nav_infrastructure()}
					description={m.dashboard_hub_infra_description()}
					icon={Boxes}
					href="/infrastructure"
					status={infra.status}
					attention={infraAttention}
					links={[
						{ label: m.nav_infrastructure(), href: '/infrastructure' },
						{ label: m.dashboard_hub_logs(), href: '/infrastructure?view=logs' }
					]}
				>
					{#if infra.data}
						{@const d = infra.data}
						{#if !d.kubernetes}
							<p class="text-xs text-muted-content">
								{d.message || m.dashboard_hub_infra_not_kubernetes()}
							</p>
						{:else}
							<div class="grid grid-cols-2 gap-3">
								<HubStat
									label={m.dashboard_hub_problems()}
									value={d.errors + d.warnings}
									tone={d.errors > 0 ? 'error' : 'warning'}
								/>
								<div class="flex min-w-0 flex-col gap-1">
									<div class="truncate text-[11px] tracking-wide text-muted-content">
										{m.dashboard_hub_workloads_healthy()}
									</div>
									<div class="text-xl font-semibold tabular-nums leading-none">
										{d.healthy}<span class="text-sm font-normal text-muted-content">
											/ {d.total}</span
										>
									</div>
								</div>
							</div>
							<SegmentBar
								segments={[
									{
										label: m.dashboard_hub_health_healthy(),
										value: d.healthy,
										color: 'bg-success'
									},
									{
										label: m.dashboard_hub_health_starting(),
										value: d.starting,
										color: 'bg-primary'
									},
									{ label: m.dashboard_hub_health_failing(), value: d.failing, color: 'bg-error' },
									{
										label: m.dashboard_hub_health_stopped(),
										value: d.stopped,
										color: 'bg-base-400'
									}
								]}
							/>
						{/if}
					{/if}
				</HubCard>

				<HubCard
					id="skills"
					title={m.nav_skills()}
					description={m.dashboard_hub_skills_description()}
					icon={PencilRuler}
					href="/skills"
					status={skills.status}
					attention={skillsAttention}
					links={[{ label: m.nav_skills(), href: '/skills' }]}
				>
					{#if skills.data}
						<div class="grid grid-cols-2 gap-3">
							<HubStat label={m.dashboard_hub_skills()} value={skills.data.skills} />
							<HubStat label={m.dashboard_hub_skill_sources()} value={skills.data.sources} />
						</div>
					{/if}
				</HubCard>

				<HubCard
					id="identity"
					title={m.nav_identity_access()}
					description={m.dashboard_hub_identity_description()}
					icon={Users}
					href="/identity-access"
					status={users.status}
					links={[
						{ label: m.dashboard_hub_users(), href: '/identity-access?view=users' },
						{ label: m.dashboard_hub_agents(), href: '/identity-access?view=agents' }
					]}
				>
					{#if users.data}
						<div class="grid grid-cols-3 gap-3">
							<HubStat label={m.dashboard_hub_users()} value={users.data.users} />
							<HubStat label={m.dashboard_hub_active_users()} value={users.data.active} />
							<HubStat label={m.dashboard_hub_new_users()} value={users.data.newUsers} />
						</div>
					{/if}
				</HubCard>

				<HubCard
					id="platform"
					title={m.nav_platform()}
					description={m.dashboard_hub_platform_description()}
					icon={Settings2}
					href="/admin/platform"
					status={hasAdminAccess ? 'ready' : 'off'}
					attention={platformAttention}
					links={[
						{ label: m.dashboard_hub_license(), href: '/admin/platform?view=license' },
						{ label: m.dashboard_hub_settings(), href: '/admin/platform?view=settings' }
					]}
				>
					<dl class="grid grid-cols-2 gap-3 text-sm">
						<div class="flex min-w-0 flex-col gap-1">
							<dt class="text-[11px] tracking-wide text-muted-content">
								{m.dashboard_hub_version()}
							</dt>
							<dd class="truncate font-semibold">{version.current.obot || '—'}</dd>
						</div>
						<div class="flex min-w-0 flex-col gap-1">
							<dt class="text-[11px] tracking-wide text-muted-content">
								{m.dashboard_hub_engine()}
							</dt>
							<dd class="truncate font-semibold capitalize">{version.current.engine || '—'}</dd>
						</div>
						{#if version.current.userLimit}
							<div class="col-span-2 flex min-w-0 flex-col gap-1">
								<dt class="text-[11px] tracking-wide text-muted-content">
									{m.dashboard_hub_licensed_users()}
								</dt>
								<dd class="font-semibold tabular-nums">
									{version.current.userCount ?? 0}<span class="font-normal text-muted-content">
										/ {version.current.userLimit}</span
									>
								</dd>
							</div>
						{/if}
					</dl>
				</HubCard>

				<HubCard
					id="ai"
					title={m.nav_ai()}
					description={m.dashboard_hub_ai_description()}
					icon={Sparkles}
					href="/ai"
					status="ready"
					links={[{ label: m.dashboard_hub_open_ai(), href: '/ai' }]}
				>
					<ul class="flex flex-col gap-1.5">
						{#each suggestions as question (question)}
							<li>
								<a
									href={resolve(`/ai?ask=${encodeURIComponent(question)}`)}
									class="flex items-center gap-2 rounded-md bg-base-200 px-3 py-2 text-xs transition-colors hover:bg-primary/10 hover:text-primary dark:bg-base-300"
								>
									<Sparkles class="size-3.5 shrink-0 text-primary" />
									<span class="truncate">{question}</span>
								</a>
							</li>
						{/each}
					</ul>
				</HubCard>
			</div>
		</section>

		<!-- The original Dashboard's charts -->
		<section aria-labelledby="hub-trends-title" class="flex flex-col gap-3">
			<h2 id="hub-trends-title" class="text-sm font-semibold">
				{m.dashboard_hub_trends_title()}
			</h2>
			<DashboardTrends />
		</section>
	</div>
</Layout>
