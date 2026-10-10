<script lang="ts">
	import CopyButton from '$lib/components/CopyButton.svelte';
	import JsonPreview from '$lib/components/JsonPreview.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import { isAbortError } from '$lib/errors';
	import {
		formatCost,
		formatNumber,
		getLog,
		type GatewayLog
	} from '$lib/services/llm-gateway/gateway';
	import { providerLabel } from '$lib/services/llm-gateway/providers';
	import { userDeviceSettings } from '$lib/stores';
	import { formatLogTimestamp } from '$lib/time';
	import { CircleAlert, X } from '@lucide/svelte';
	import { twMerge } from 'tailwind-merge';

	interface Props {
		log: GatewayLog;
		onClose: () => void;
	}

	let { log, onClose }: Props = $props();
	let loading = $state(true);
	let fetchError = $state<string | null>(null);
	let full = $state<GatewayLog>();

	$effect(() => {
		const id = log.id;
		if (!id) return;
		const controller = new AbortController();
		loading = true;
		fetchError = null;
		full = undefined;
		getLog(id, { signal: controller.signal })
			.then((response) => {
				if (!controller.signal.aborted) full = response;
			})
			.catch((err) => {
				if (isAbortError(err) || controller.signal.aborted) return;
				fetchError = err instanceof Error ? err.message : 'Failed to load request details';
			})
			.finally(() => {
				if (!controller.signal.aborted) loading = false;
			});
		return () => controller.abort();
	});

	let data = $derived<GatewayLog>({ ...log, ...full });
	let user = $derived(data.metadata?.['obot-user-name'] ?? data.metadata?.['obot-user-id'] ?? '—');

	let properties = $derived([
		['Status', data.status],
		['Type', `${data.object}${data.stream ? ' (stream)' : ''}`],
		['Provider', providerLabel(data.provider)],
		['Model', data.model || '—'],
		['User', user],
		['Key', data.selected_key_name ?? '—'],
		['Routing rule', data.routing_rule_name ?? '—'],
		['Retries', String(data.number_of_retries ?? 0)],
		['Fallback', data.fallback_index ? `#${data.fallback_index}` : 'No'],
		['Prompt tokens', formatNumber(data.token_usage?.prompt_tokens)],
		['Completion tokens', formatNumber(data.token_usage?.completion_tokens)],
		['Cost', formatCost(data.cost)],
		['Latency', data.latency ? `${Math.round(data.latency * 1000)} ms` : '—']
	]);

	function parsed(value: unknown) {
		if (typeof value === 'string') {
			try {
				return JSON.parse(value);
			} catch {
				return value;
			}
		}
		return value;
	}

	function hasValue(value: unknown) {
		if (value === undefined || value === null || value === '') return false;
		if (Array.isArray(value)) return value.length > 0;
		if (typeof value === 'object') return Object.keys(value).length > 0;
		return true;
	}

	let blocks = $derived(
		(
			[
				['Input', data.input_history?.length ? data.input_history : data.responses_input_history],
				['Output', data.output_message ?? data.responses_output],
				['Parameters', data.params],
				['Metadata', data.metadata]
			] as [string, unknown][]
		)
			.map(([label, value]) => [label, parsed(value)] as [string, unknown])
			.filter(([, value]) => hasValue(value))
	);
</script>

<div
	class="bg-base-200 text-base-content flex h-full w-[inherit] min-w-[inherit] flex-col"
	role="dialog"
	aria-label="Request details"
>
	<div class="dark:bg-base-300 bg-base-100 relative flex w-full flex-col p-4 pl-5 shadow-xs">
		<div
			class={twMerge(
				'absolute top-0 left-0 h-full w-1',
				data.status === 'error' ? 'bg-error' : 'bg-primary'
			)}
		></div>
		<h3 class="text-lg font-semibold">
			{formatLogTimestamp(data.timestamp, userDeviceSettings.timeFormat)}
		</h3>
		<p class="text-muted-content text-xs font-light">{data.id}</p>
		<IconButton
			onclick={onClose}
			class="absolute top-1/2 right-4 -translate-y-1/2"
			tooltip={{ text: 'Close' }}
		>
			<X class="size-5" />
		</IconButton>
	</div>
	<div class="default-scrollbar-thin relative h-[calc(100%-60px)] overflow-y-auto pb-4">
		<div class="bg-base-300 absolute top-0 left-0 h-full w-1"></div>

		<div class="flex flex-wrap gap-2 p-4 pl-5">
			{#each properties as [label, value] (label)}
				<div class="bg-base-400 truncate rounded-full px-3 py-1 text-[11px] font-light">
					<span class="font-medium">{label}:</span>
					<span>{value}</span>
				</div>
			{/each}
		</div>

		<div class="flex flex-col gap-4 px-5">
			{#if fetchError}
				<div class="notification-error flex items-center gap-3 p-3">
					<CircleAlert class="size-4 shrink-0" />
					<div class="flex flex-col gap-1">
						<p class="text-sm font-semibold">Unable to load full request details</p>
						<p class="text-sm font-light">{fetchError}</p>
					</div>
				</div>
			{/if}
			{#if data.error_details?.error?.message}
				<div class="flex flex-col gap-0.5">
					<p class="mb-2 text-base font-semibold">Error</p>
					<p class="text-error text-sm">{data.error_details.error.message}</p>
				</div>
			{/if}
			{#if loading}
				<div class="skeleton h-32 w-full rounded-md"></div>
			{/if}
			{#each blocks as [label, value] (label)}
				<div class="flex flex-col gap-0.5">
					<p class="flex items-center gap-2 text-base font-semibold">
						{label}
						<CopyButton
							text={typeof value === 'string' ? value : JSON.stringify(value, null, 2)}
							classes={{ button: 'text-xs font-normal flex items-center gap-1' }}
						/>
					</p>
					<div class="relative mt-2">
						<JsonPreview {value} ariaLabel={`${label} JSON`} maximizable />
					</div>
				</div>
			{/each}
		</div>
	</div>
</div>
