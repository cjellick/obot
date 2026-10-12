<script lang="ts">
	import { afterNavigate } from '$app/navigation';
	import GatewayConnect from '$lib/components/llm-gateway/GatewayConnect.svelte';
	import { PAGE_TRANSITION_DURATION } from '$lib/constants';
	import { doGet } from '$lib/services/http';
	import accessibleModels from '$lib/stores/accessibleModels.svelte';
	import { onMount } from 'svelte';
	import { fade } from 'svelte/transition';

	let obotURL = $state('');
	// The store only holds the LLM gateway's models.
	let gatewayModels = $derived(accessibleModels.current);
	let subscriptionPassthrough = $state(false);

	onMount(() => {
		obotURL = window.location.origin;
		// Without it, the setup instructions just leave out the Claude subscription option.
		(
			doGet('/llm-gateway/settings', { dontLogErrors: true }) as Promise<{
				subscriptionPassthrough: boolean;
			}>
		)
			.then((s) => (subscriptionPassthrough = s.subscriptionPassthrough))
			.catch(() => {});
	});

	afterNavigate(({ type }) => {
		if (type !== 'enter') {
			void accessibleModels.refresh();
		}
	});

	let ready = $derived(obotURL !== '');

	const duration = PAGE_TRANSITION_DURATION;
</script>

<div class="flex h-full w-full flex-col gap-6" in:fade={{ duration }}>
	{#if ready}
		<div class="flex flex-col gap-4">
			{#if gatewayModels.length > 0}
				<div class="paper flex flex-col gap-4 p-4">
					<div class="flex items-center gap-3">
						<img src="/admin/assets/llm_gateway_icon.svg" alt="" class="size-8 rounded-md" />
						<div class="flex flex-col">
							<h3 class="text-base font-semibold">LLM Gateway</h3>
							<p class="text-muted-content text-xs">
								{gatewayModels.length} models across every configured provider, in any API format.
							</p>
						</div>
					</div>
					<GatewayConnect {obotURL} models={gatewayModels} {subscriptionPassthrough} />
					<details class="text-sm">
						<summary class="cursor-pointer font-medium">Available models</summary>
						<div class="mt-2 flex flex-wrap gap-1.5">
							{#each gatewayModels as m (m.id)}
								<span class="bg-base-200 dark:bg-base-300 rounded px-2 py-0.5 font-mono text-xs"
									>{m.targetModel}</span
								>
							{/each}
						</div>
					</details>
				</div>
			{/if}
		</div>
	{/if}
</div>
