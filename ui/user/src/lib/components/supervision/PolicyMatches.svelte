<script lang="ts">
	import { m } from '$lib/i18n';
	import { listEvents, type ProvenanceEvent } from '$lib/services/provenance';
	import { formatTimeAgo } from '$lib/time';
	import { resolve } from '$lib/url';
	import DecisionPill from './DecisionPill.svelte';
	import { onMount } from 'svelte';

	// The actions a policy recently decided or, in shadow mode, would have.

	let { policyID }: { policyID: string } = $props();

	let events = $state<ProvenanceEvent[]>([]);
	let total = $state(0);
	let loading = $state(true);

	onMount(async () => {
		try {
			const resp = await listEvents({ policyId: policyID, limit: 10 }, { dontLogErrors: true });
			events = resp.items ?? [];
			total = resp.total ?? events.length;
		} catch {
			events = [];
		} finally {
			loading = false;
		}
	});
</script>

<section class="paper gap-3" aria-label={m.supervision_policy_matches_title()}>
	<div class="flex items-baseline justify-between gap-2">
		<h2 class="text-base font-semibold">{m.supervision_policy_matches_title()}</h2>
		{#if total > events.length}
			<span class="text-muted-content text-xs"
				>{m.supervision_policy_matches_more({ shown: events.length, total })}</span
			>
		{/if}
	</div>
	{#if loading}
		<p class="text-muted-content animate-pulse text-sm">…</p>
	{:else if events.length === 0}
		<p class="text-muted-content text-sm">{m.supervision_policy_matches_empty()}</p>
	{:else}
		<ul class="flex flex-col divide-y divide-base-300">
			{#each events as ev (ev.id)}
				<li class="flex flex-wrap items-center gap-3 py-2 text-sm">
					<span class="text-muted-content w-24 shrink-0 text-xs"
						>{formatTimeAgo(ev.at).relativeTime}</span
					>
					<span class="min-w-0 flex-1 break-all">{ev.summary || ev.kind}</span>
					<DecisionPill decision={ev.decision} />
					<a class="link text-xs" href={resolve(`/sessions/${ev.sessionID}`)}
						>{m.supervision_policy_matches_open_session()}</a
					>
				</li>
			{/each}
		</ul>
	{/if}
</section>
