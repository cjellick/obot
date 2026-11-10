<script lang="ts">
	import { m } from '$lib/i18n';
	import type { InfraPod, InfraSection } from '$lib/services/admin/infrastructure';
	import { formatTimeAgo } from '$lib/time';
	import { resolve } from '$lib/url';
	import HealthBadge from './HealthBadge.svelte';
	import { ArrowRight, User } from '@lucide/svelte';
	import { twMerge } from 'tailwind-merge';

	let { section }: { section: InfraSection } = $props();

	const sessionCount = $derived(
		section.workloads.reduce(
			(n, w) => n + w.pods.reduce((p, pod) => p + (pod.sessions?.length ?? 0), 0),
			0
		)
	);

	function podTone(pod: InfraPod) {
		if (pod.status === 'Running' && pod.ready.split('/')[0] === pod.ready.split('/')[1])
			return 'bg-success';
		if (pod.phase === 'Pending' || pod.status === 'ContainerCreating') return 'bg-warning';
		return pod.status === 'Running' ? 'bg-warning' : 'bg-error';
	}
</script>

<section class="flex flex-col gap-2" aria-labelledby="infra-overview-claude">
	<div class="flex items-center justify-between gap-2">
		<h2 id="infra-overview-claude" class="text-lg font-semibold">
			{m.infrastructure_tab_claude()}
		</h2>
		{#if section.enabled}
			<a
				class="link text-primary flex items-center gap-1 text-sm"
				href={resolve('/infrastructure?view=claude')}
			>
				{m.infrastructure_view_all_claude()}
				<ArrowRight class="size-4" />
			</a>
		{/if}
	</div>

	{#if !section.enabled}
		<div class="paper text-muted-content p-4 text-sm">{m.infrastructure_claude_disabled()}</div>
	{:else if section.workloads.length === 0}
		<div class="paper text-muted-content p-4 text-sm">
			{m.infrastructure_overview_no_claude()}
		</div>
	{:else}
		<p class="text-muted-content text-xs">
			{m.infrastructure_overview_claude_summary({
				pools: section.workloads.length,
				sessions: sessionCount
			})}
		</p>
		<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
			{#each section.workloads as w (w.name)}
				<div class="paper flex flex-col gap-3 p-4">
					<div class="flex flex-wrap items-start justify-between gap-2">
						<div class="flex min-w-0 flex-col">
							<span class="truncate font-semibold">
								{w.environmentName || w.displayName || w.name}
							</span>
							<span class="text-muted-content truncate font-mono text-xs">{w.name}</span>
						</div>
						<div class="flex items-center gap-2">
							<span class="pill-secondary"
								>{m.infrastructure_overview_workers({
									ready: w.readyReplicas,
									total: w.replicas
								})}</span
							>
							<HealthBadge health={w.health} />
						</div>
					</div>

					{#if w.pods.length === 0}
						<p class="text-muted-content text-sm">{m.infrastructure_no_pods()}</p>
					{:else}
						<ul class="flex flex-col divide-y divide-base-300">
							{#each w.pods as pod (pod.name)}
								<li class="flex flex-col gap-1.5 py-2 first:pt-0 last:pb-0">
									<div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
										<span class={twMerge('size-2 shrink-0 rounded-full', podTone(pod))}></span>
										<span class="min-w-0 truncate font-mono text-xs">{pod.name}</span>
										<span class="text-muted-content text-xs">{pod.status}</span>
										{#if pod.restarts > 0}
											<span class="pill-warning"
												>{m.infrastructure_overview_restarts({ count: pod.restarts })}</span
											>
										{/if}
										<span class="text-muted-content ml-auto text-xs"
											>{formatTimeAgo(pod.created).relativeTime}</span
										>
									</div>
									{#if pod.sessions?.length}
										<div class="flex flex-wrap gap-1.5 pl-4">
											{#each pod.sessions as s (s.id)}
												<span class="pill-rounded max-w-full">
													<User class="size-3 shrink-0" />
													<span class="font-medium">{s.userName || s.userID || '—'}</span>
													<span class="text-muted-content truncate">{s.name || s.id}</span>
													{#if s.state}
														<span class="text-muted-content">· {s.state}</span>
													{/if}
												</span>
											{/each}
										</div>
									{:else}
										<span class="text-muted-content pl-4 text-xs">{m.infrastructure_idle()}</span>
									{/if}
								</li>
							{/each}
						</ul>
					{/if}
				</div>
			{/each}
		</div>
	{/if}
</section>
