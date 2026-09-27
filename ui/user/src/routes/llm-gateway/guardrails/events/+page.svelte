<script lang="ts">
	import Layout from '$lib/components/Layout.svelte';
	import GuardrailsTabs from '$lib/components/llm-gateway/GuardrailsTabs.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import { UserService } from '$lib/services';
	import {
		APPLY_TO_LABELS,
		findingsSummary,
		listGuardrailEvents,
		OUTCOME_LABELS,
		TARGET_LABELS,
		type GuardrailEvent,
		type GuardrailOutcome,
		type GuardrailTarget
	} from '$lib/services/llm-gateway/guardrails';
	import { ChevronLeft, ChevronRight, RefreshCw } from '@lucide/svelte';
	import { onMount } from 'svelte';

	const pageSize = 50;

	let events = $state<GuardrailEvent[]>([]);
	let total = $state(0);
	let offset = $state(0);
	let loading = $state(true);
	let outcome = $state<GuardrailOutcome | ''>('');
	let target = $state<GuardrailTarget | ''>('');
	let userNames = $state<Record<string, string>>({});
	let usersLoaded = false;

	async function load(resetOffset = false) {
		if (resetOffset) offset = 0;
		loading = true;
		try {
			const resp = await listGuardrailEvents({ limit: pageSize, offset, outcome, target });
			events = resp.items;
			total = resp.total;
			if (!usersLoaded && events.some((e) => e.userID)) {
				usersLoaded = true;
				const users = await UserService.listUsers().catch(() => []);
				userNames = Object.fromEntries(
					users.map((u) => [u.id, u.displayName || u.email || u.username || u.id])
				);
			}
		} finally {
			loading = false;
		}
	}

	onMount(() => load());

	function outcomeClass(o: GuardrailOutcome) {
		switch (o) {
			case 'blocked':
			case 'error':
				return 'bg-error/15 text-error';
			case 'redacted':
				return 'bg-warning/15 text-warning';
			default:
				return 'bg-base-300 text-base-content';
		}
	}
</script>

<svelte:head>
	<title>Obot | Guardrail Events</title>
</svelte:head>

<Layout title="Guardrails" classes={{ childrenContainer: 'max-w-none' }}>
	{#snippet rightNavActions()}
		<IconButton tooltip={{ text: 'Refresh' }} onclick={() => load()}>
			<RefreshCw class="size-4" />
		</IconButton>
	{/snippet}

	<div class="flex flex-col gap-4">
		<p class="text-muted-content max-w-4xl text-sm">
			Every time a guardrail blocked, redacted or logged traffic, or failed to evaluate. Events
			describe what was found, never the content itself.
		</p>

		<GuardrailsTabs active="events" />

		<div class="flex flex-wrap items-center gap-2">
			<select
				class="select select-sm"
				bind:value={outcome}
				onchange={() => load(true)}
				aria-label="Outcome"
			>
				<option value="">Any outcome</option>
				{#each Object.entries(OUTCOME_LABELS) as [value, label] (value)}
					<option {value}>{label}</option>
				{/each}
			</select>
			<select
				class="select select-sm"
				bind:value={target}
				onchange={() => load(true)}
				aria-label="Target"
			>
				<option value="">All targets</option>
				{#each Object.entries(TARGET_LABELS) as [value, label] (value)}
					<option {value}>{label}</option>
				{/each}
			</select>
		</div>

		<div class="paper overflow-x-auto p-0">
			<table class="table-sm table w-full text-sm">
				<thead>
					<tr class="text-muted-content text-xs">
						<th class="text-left">Time</th>
						<th class="text-left">Rule</th>
						<th class="text-left">Profile</th>
						<th class="text-left">Target / stage</th>
						<th class="text-left">Outcome</th>
						<th class="text-left">User</th>
						<th class="text-left">Model / resource</th>
						<th class="text-left">Findings</th>
					</tr>
				</thead>
				<tbody>
					{#if loading}
						<tr><td colspan="8" class="p-8 text-center"><Loading class="mx-auto size-6" /></td></tr>
					{:else if events.length === 0}
						<tr
							><td colspan="8" class="text-muted-content p-8 text-center"
								>No guardrail events match.</td
							></tr
						>
					{:else}
						{#each events as ev (ev.id)}
							<tr>
								<td class="whitespace-nowrap">{new Date(ev.created).toLocaleString()}</td>
								<td>{ev.ruleName || ev.ruleID}</td>
								<td>{ev.profileName || '—'}</td>
								<td class="text-xs whitespace-nowrap">
									{TARGET_LABELS[ev.target] ?? ev.target} · {APPLY_TO_LABELS[ev.stage] ?? ev.stage}
								</td>
								<td>
									<span class={`rounded-full px-2 py-0.5 text-xs ${outcomeClass(ev.outcome)}`}>
										{OUTCOME_LABELS[ev.outcome] ?? ev.outcome}
									</span>
								</td>
								<td class="truncate">
									{ev.userID ? (userNames[ev.userID] ?? ev.userID) : '—'}
								</td>
								<td class="text-xs">{ev.model || ev.resource || '—'}</td>
								<td class="text-xs">
									{#if ev.outcome === 'error'}
										<span class="text-error">{ev.error}</span>
									{:else}
										{findingsSummary(ev.findings) || '—'}
									{/if}
								</td>
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
