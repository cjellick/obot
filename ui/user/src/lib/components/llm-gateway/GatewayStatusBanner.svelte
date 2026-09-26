<script lang="ts">
	import { resolve } from '$app/paths';
	import type { GatewayStatus } from '$lib/services/llm-gateway/gateway';
	import { CircleAlert, Info, LoaderCircle } from '@lucide/svelte';

	interface Props {
		status?: GatewayStatus;
		showNoProviders?: boolean;
	}

	let { status, showNoProviders = true }: Props = $props();
</script>

{#if status && !status.enabled}
	<div class="notification-alert flex items-center gap-2 rounded-md p-3 text-sm">
		<CircleAlert class="size-4 shrink-0" />
		The LLM gateway is disabled on this Obot server (<code>--disable-llm-gateway</code>).
	</div>
{:else if status?.error}
	<div class="notification-error flex items-center gap-2 rounded-md p-3 text-sm">
		<CircleAlert class="size-4 shrink-0" />
		The LLM gateway failed to start: {status.error}
	</div>
{:else if status && !status.ready}
	<div class="notification-info flex items-center gap-2 rounded-md p-3 text-sm">
		<LoaderCircle class="size-4 shrink-0 animate-spin" />
		The LLM gateway is starting…
	</div>
{:else if status && showNoProviders && !status.hasProviders}
	<div class="notification-info flex items-center gap-2 rounded-md p-3 text-sm">
		<Info class="size-4 shrink-0" />
		<span>
			No upstream providers are configured yet.
			<a href={resolve('/llm-gateway/providers')} class="text-link">Add a provider</a> to start routing
			requests.
		</span>
	</div>
{/if}
