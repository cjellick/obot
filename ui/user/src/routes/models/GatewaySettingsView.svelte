<script lang="ts">
	import Select from '$lib/components/Select.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import GatewayStatusBanner from '$lib/components/llm-gateway/GatewayStatusBanner.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import { doGet, doPut } from '$lib/services/http';
	import {
		getStatus,
		listPlugins,
		listProviders,
		upsertPlugin,
		type ClientConfig,
		type GatewayStatus,
		type Plugin,
		type Provider
	} from '$lib/services/llm-gateway/gateway';
	import { providerLabel } from '$lib/services/llm-gateway/providers';
	import { profile } from '$lib/stores';
	import { success } from '$lib/stores/success';
	import { onMount } from 'svelte';

	const CACHE_PLUGIN = 'semantic_cache';

	type FullConfig = {
		client_config: ClientConfig;
		framework_config: Record<string, unknown>;
		is_cache_connected: boolean;
		is_logs_connected: boolean;
	};

	let status = $state<GatewayStatus>();
	let config = $state<FullConfig>();
	let plugins = $state<Plugin[]>([]);
	let providers = $state<Provider[]>([]);
	let loading = $state(true);
	let saving = $state(false);

	// Cache settings.
	let cacheEnabled = $state(false);
	let cacheMode = $state<'direct' | 'semantic'>('direct');
	let embeddingProvider = $state('openai');
	let embeddingModel = $state('text-embedding-3-small');
	let dimension = $state(1536);
	let ttl = $state('5m');
	let threshold = $state(0.8);
	let cacheByModel = $state(true);
	let cacheByProvider = $state(true);
	let excludeSystemPrompt = $state(false);

	let readonly = $derived(profile.current.isAdminReadonly?.());
	let cachePlugin = $derived(plugins.find((p) => p.name === CACHE_PLUGIN));
	let providerOptions = $derived(
		providers.map((p) => ({ id: p.name, label: providerLabel(p.name) }))
	);

	async function load() {
		loading = true;
		try {
			status = await getStatus();
			if (!status.ready) return;
			[config, plugins, providers] = await Promise.all([
				doGet('/llm-gateway/admin/config') as Promise<FullConfig>,
				listPlugins(),
				listProviders()
			]);
			const c = (cachePlugin?.config ?? {}) as Record<string, unknown>;
			cacheEnabled = !!cachePlugin?.enabled;
			cacheMode = c.provider ? 'semantic' : 'direct';
			embeddingProvider = (c.provider as string) ?? 'openai';
			embeddingModel = (c.embedding_model as string) ?? 'text-embedding-3-small';
			dimension = (c.dimension as number) > 1 ? (c.dimension as number) : 1536;
			ttl = String(c.ttl ?? '5m');
			threshold = (c.threshold as number) ?? 0.8;
			cacheByModel = (c.cache_by_model as boolean) ?? true;
			cacheByProvider = (c.cache_by_provider as boolean) ?? true;
			excludeSystemPrompt = (c.exclude_system_prompt as boolean) ?? false;
		} finally {
			loading = false;
		}
	}

	onMount(load);

	function cacheConfig() {
		const cfg: Record<string, unknown> = {
			ttl,
			cache_by_model: cacheByModel,
			cache_by_provider: cacheByProvider,
			exclude_system_prompt: excludeSystemPrompt,
			conversation_history_threshold: 3,
			vector_store_namespace: 'ObotLLMGatewayCache',
			// Obot sets a per-user cache key on every request, so entries are never shared
			// between users. A default key would make caching apply to requests without one.
			default_cache_key: ''
		};
		if (cacheMode === 'semantic') {
			cfg.provider = embeddingProvider;
			cfg.embedding_model = embeddingModel;
			cfg.dimension = Number(dimension);
			cfg.threshold = Number(threshold);
		} else {
			cfg.dimension = 1;
		}
		return cfg;
	}

	async function save() {
		if (!config) return;
		saving = true;
		try {
			await doPut('/llm-gateway/admin/config', {
				client_config: config.client_config,
				framework_config: config.framework_config
			});
			if (config.is_cache_connected) {
				await upsertPlugin(
					{ name: CACHE_PLUGIN, enabled: cacheEnabled, config: cacheConfig() },
					!!cachePlugin
				);
			}
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
				<h2 class="text-lg font-semibold">Response caching</h2>
				<p class="text-muted-content text-sm">
					Cached responses are kept per user and never shared between users. Clients can send
					<code>X-Obot-Cache-TTL</code> or <code>X-Obot-Cache-No-Store</code> to tune a request.
				</p>
			</div>
			{#if !config.is_cache_connected}
				<div class="notification-info rounded-md p-3 text-sm">
					Caching needs a Redis or Valkey server. Start Obot with
					<code>--llm-gateway-cache-redis-addr</code> (or
					<code>OBOT_SERVER_LLM_GATEWAY_CACHE_REDIS_ADDR</code>) to enable it.
				</div>
			{:else}
				<Toggle
					label="Enable caching"
					labelInline
					checked={cacheEnabled}
					disabled={readonly}
					onChange={(v) => (cacheEnabled = v)}
				/>
				<div class="flex gap-4 text-sm">
					<label class="flex items-center gap-2">
						<input
							type="radio"
							class="radio radio-sm"
							value="direct"
							bind:group={cacheMode}
							disabled={readonly}
						/>
						Exact match
					</label>
					<label class="flex items-center gap-2">
						<input
							type="radio"
							class="radio radio-sm"
							value="semantic"
							bind:group={cacheMode}
							disabled={readonly}
						/>
						Exact + semantic similarity
					</label>
				</div>
				<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
					<div class="flex flex-col gap-1">
						<label for="c-ttl" class="text-sm font-light">Time to live</label>
						<input
							id="c-ttl"
							class="text-input-filled"
							bind:value={ttl}
							placeholder="5m"
							disabled={readonly}
						/>
					</div>
					{#if cacheMode === 'semantic'}
						<div class="flex flex-col gap-1">
							<label for="c-threshold" class="text-sm font-light">Similarity threshold (0–1)</label>
							<input
								id="c-threshold"
								type="number"
								min="0"
								max="1"
								step="0.01"
								class="text-input-filled"
								bind:value={threshold}
								disabled={readonly}
							/>
						</div>
						<div class="flex flex-col gap-1">
							<span id="c-provider-label" class="text-sm font-light">Embedding provider</span>
							<Select
								id="c-provider"
								class="bg-base-200 dark:bg-base-200 dark:border-base-400 border border-transparent shadow-inner"
								ariaLabelledby="c-provider-label"
								options={providerOptions}
								selected={embeddingProvider}
								onSelect={(option) => (embeddingProvider = option.id)}
								disabled={readonly}
							/>
						</div>
						<div class="flex flex-col gap-1">
							<label for="c-model" class="text-sm font-light">Embedding model</label>
							<input
								id="c-model"
								class="text-input-filled"
								bind:value={embeddingModel}
								disabled={readonly}
							/>
						</div>
						<div class="flex flex-col gap-1">
							<label for="c-dim" class="text-sm font-light">Embedding dimension</label>
							<input
								id="c-dim"
								type="number"
								class="text-input-filled"
								bind:value={dimension}
								disabled={readonly}
							/>
						</div>
					{/if}
				</div>
				<div class="flex flex-wrap gap-6">
					<Toggle
						label="Separate by model"
						labelInline
						checked={cacheByModel}
						disabled={readonly}
						onChange={(v) => (cacheByModel = v)}
					/>
					<Toggle
						label="Separate by provider"
						labelInline
						checked={cacheByProvider}
						disabled={readonly}
						onChange={(v) => (cacheByProvider = v)}
					/>
					<Toggle
						label="Ignore system prompt"
						labelInline
						checked={excludeSystemPrompt}
						disabled={readonly}
						onChange={(v) => (excludeSystemPrompt = v)}
					/>
				</div>
				{#if cachePlugin?.status?.status}
					<span class="text-muted-content text-xs">Plugin status: {cachePlugin.status.status}</span>
				{/if}
			{/if}
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
