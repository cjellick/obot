<script lang="ts">
	import { parseErrorContent } from '$lib/errors';
	import { m } from '$lib/i18n';
	import { allowEgressHost, listBlockedEgress, type BlockedEgress } from '$lib/services/ai';
	import WidgetFrame from './WidgetFrame.svelte';
	import { formatDistanceToNow } from 'date-fns';
	import { onMount } from 'svelte';

	// Hosts Claude environment sessions were denied, with an Allow button the person clicks.

	let items = $state<BlockedEgress[]>([]);
	let error = $state('');
	let loading = $state(true);
	let busy = $state('');
	let rowError = $state<Record<string, string>>({});

	const key = (b: BlockedEgress) => `${b.environmentID}|${b.host}`;

	async function load() {
		try {
			items = await listBlockedEgress(7, { dontLogErrors: true });
		} catch (e) {
			error = parseErrorContent(e).message;
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function allow(b: BlockedEgress) {
		busy = key(b);
		rowError = { ...rowError, [key(b)]: '' };
		try {
			await allowEgressHost(b.environmentID, b.host);
			await load();
		} catch (e) {
			rowError = { ...rowError, [key(b)]: parseErrorContent(e).message };
		} finally {
			busy = '';
		}
	}
</script>

<WidgetFrame {loading} {error}>
	<p class="text-xs font-semibold uppercase">{m.ai_widget_egress_title()}</p>
	{#if items.length === 0}
		<p class="text-muted-content text-sm">{m.ai_widget_egress_empty()}</p>
	{:else}
		<ul class="flex flex-col gap-2">
			{#each items as b (key(b))}
				<li class="bg-base-100 dark:bg-base-200 flex flex-wrap items-center gap-3 rounded-lg p-3">
					<div class="flex min-w-0 flex-1 flex-col gap-0.5">
						<span class="font-mono text-sm font-medium break-all">{b.host}</span>
						<span class="text-muted-content text-xs">
							{m.ai_widget_egress_detail({
								environment: b.environment || b.environmentID,
								count: b.count,
								when: formatDistanceToNow(new Date(b.lastSeen), { addSuffix: true })
							})}
							{#if b.users.length}· {b.users.join(', ')}{/if}
						</span>
						{#if rowError[key(b)]}
							<span class="text-error text-xs" role="alert">{rowError[key(b)]}</span>
						{/if}
					</div>
					{#if b.allowedNow?.length}
						<span class="badge badge-sm badge-soft badge-success whitespace-nowrap">
							{m.ai_widget_egress_allowed_now({ where: b.allowedNow.join(', ') })}
						</span>
					{:else}
						<button
							class="btn btn-primary btn-sm"
							disabled={busy !== ''}
							onclick={() => allow(b)}
							aria-label={m.ai_widget_egress_allow_label({ host: b.host })}
						>
							{m.ai_widget_egress_allow()}
						</button>
					{/if}
				</li>
			{/each}
		</ul>
		<p class="text-muted-content text-xs">{m.ai_widget_egress_note()}</p>
	{/if}
</WidgetFrame>
