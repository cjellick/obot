<script lang="ts">
	import Skeleton from '$lib/components/Skeleton.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import GatewayStatusBanner from '$lib/components/llm-gateway/GatewayStatusBanner.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import { doGet, doPut } from '$lib/services/http';
	import {
		getStatus,
		type ClientConfig,
		type GatewayStatus
	} from '$lib/services/llm-gateway/gateway';
	import { profile } from '$lib/stores';
	import { success } from '$lib/stores/success';
	import { onMount } from 'svelte';

	// Settings Obot enforces itself, rather than the engine's configuration.
	type ObotSettings = { subscriptionPassthrough: boolean };

	type FullConfig = {
		client_config: ClientConfig;
		framework_config: Record<string, unknown>;
		is_logs_connected: boolean;
	};

	let status = $state<GatewayStatus>();
	let config = $state<FullConfig>();
	let obotSettings = $state<ObotSettings>({ subscriptionPassthrough: false });
	let loading = $state(true);
	let saving = $state(false);

	let readonly = $derived(profile.current.isAdminReadonly?.());

	async function load() {
		loading = true;
		try {
			status = await getStatus();
			if (!status.ready) return;
			[config, obotSettings] = await Promise.all([
				doGet('/llm-gateway/admin/config') as Promise<FullConfig>,
				doGet('/llm-gateway/settings') as Promise<ObotSettings>
			]);
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function save() {
		if (!config) return;
		saving = true;
		try {
			await doPut('/llm-gateway/admin/config', {
				client_config: config.client_config,
				framework_config: config.framework_config
			});
			await doPut('/llm-gateway/settings', obotSettings);
			success.add('Gateway settings saved.');
			await load();
		} finally {
			saving = false;
		}
	}
</script>

<div class="flex flex-col gap-6">
	<GatewayStatusBanner {status} showNoProviders={false} />

	{#if loading}
		<Skeleton type="items" count={4} class="h-32" />
	{:else if config}
		<section class="paper">
			<div>
				<h2 class="text-lg font-semibold">Logging &amp; traffic</h2>
				<p class="text-muted-content text-sm">
					What the gateway records about each request and how it handles overload.
				</p>
			</div>
			<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
				<Toggle
					label="Log requests"
					labelInline
					checked={config.client_config.enable_logging}
					disabled={readonly}
					onChange={(v) => config && (config.client_config.enable_logging = v)}
				/>
				<Toggle
					label="Store request and response content"
					labelInline
					checked={!config.client_config.disable_content_logging}
					disabled={readonly}
					onChange={(v) => config && (config.client_config.disable_content_logging = !v)}
				/>
				<div class="flex flex-col gap-1">
					<label for="s-retention" class="text-sm font-light">Log retention (days)</label>
					<input
						id="s-retention"
						type="number"
						min="0"
						class="text-input-filled"
						bind:value={config.client_config.log_retention_days}
						disabled={readonly}
					/>
				</div>
				<div class="flex flex-col gap-1">
					<label for="s-body" class="text-sm font-light">Max request body (MB)</label>
					<input
						id="s-body"
						type="number"
						min="1"
						class="text-input-filled"
						bind:value={config.client_config.max_request_body_size_mb}
						disabled={readonly}
					/>
				</div>
				<div class="flex flex-col gap-1 md:col-span-2">
					<Toggle
						label="Reject requests when a provider's queue is full"
						labelInline
						checked={config.client_config.drop_excess_requests}
						disabled={readonly}
						onChange={(v) => config && (config.client_config.drop_excess_requests = v)}
					/>
					<span class="text-muted-content text-xs">
						Off: requests wait for a free worker. On: they fail fast with 503, which lets clients
						retry elsewhere.
					</span>
				</div>
			</div>
		</section>

		<section class="paper">
			<div>
				<h2 class="text-lg font-semibold">Claude subscriptions</h2>
				<p class="text-muted-content text-sm">
					Let Claude Code users signed in with a Claude Pro, Max, Team or Enterprise subscription
					use it through the gateway. Their requests to Claude models are billed to their
					subscription instead of a configured provider key. Obot still requires their Obot API key,
					which they send in the <code>X-Obot-Api-Key</code> header, and applies model access, budgets
					and guardrails.
				</p>
			</div>
			<Toggle
				label="Allow Claude subscriptions"
				labelInline
				checked={obotSettings.subscriptionPassthrough}
				disabled={readonly}
				onChange={(v) => (obotSettings.subscriptionPassthrough = v)}
			/>
		</section>

		{#if !readonly}
			<div
				class="bg-base-200 dark:bg-base-100 sticky bottom-0 left-0 z-40 flex w-full justify-end gap-2 py-4"
			>
				<button class="btn btn-secondary text-sm" disabled={saving} onclick={load}>Cancel</button>
				<button class="btn btn-primary text-sm" disabled={saving} onclick={save}>
					{#if saving}<Loading class="size-4" />{:else}Save{/if}
				</button>
			</div>
		{/if}
	{/if}
</div>
