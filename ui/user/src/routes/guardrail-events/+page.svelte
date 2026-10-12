<script lang="ts">
	import Layout from '$lib/components/Layout.svelte';
	import EventsView from './EventsView.svelte';
	import { fade } from 'svelte/transition';
</script>

<svelte:head>
	<title>Obot | Guardrail Events</title>
</svelte:head>

<Layout title="Guardrail Events" classes={{ childrenContainer: 'max-w-none' }}>
	<div class="flex-1" in:fade={{ duration: 100 }}>
		<EventsView />
	</div>
</Layout>
