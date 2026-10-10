<script lang="ts">
	import TabLayout, { type TabView } from '$lib/components/TabLayout.svelte';
	import { profile } from '$lib/stores';
	import DetectorsView from './DetectorsView.svelte';
	import EventsView from './EventsView.svelte';
	import { Plus } from '@lucide/svelte';

	let hasAdminAccess = $derived(Boolean(profile.current.hasAdminAccess?.()));
	let isAdminReadonly = $derived(Boolean(profile.current.isAdminReadonly?.()));
	let detectorsView = $state<ReturnType<typeof DetectorsView>>();

	let views = $derived<TabView[]>(
		hasAdminAccess
			? [
					{ label: 'Detectors', value: 'detectors', content: detectors },
					{ label: 'Events', value: 'events', content: events }
				]
			: []
	);
</script>

<svelte:head>
	<title>Obot | Guardrails</title>
</svelte:head>

<TabLayout
	title="Guardrails"
	defaultView="detectors"
	rightNavActions={navActions}
	{views}
	classes={{ childrenContainer: 'max-w-none' }}
/>

{#snippet navActions(view: string)}
	{#if view === 'detectors' && hasAdminAccess && !isAdminReadonly}
		<button
			class="btn btn-primary flex items-center gap-1 text-sm"
			onclick={() => detectorsView?.openTypePicker()}
		>
			<Plus class="size-4" /> Add Detector
		</button>
	{/if}
{/snippet}

{#snippet detectors()}
	<DetectorsView bind:this={detectorsView} />
{/snippet}

{#snippet events()}
	<EventsView />
{/snippet}
