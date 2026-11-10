<script lang="ts">
	import TweenedMetric from '$lib/components/TweenedMetric.svelte';
	import { twMerge } from 'tailwind-merge';

	interface Props {
		label: string;
		value: number | undefined;
		format?: (n: number) => string;
		/** Highlights the number when it needs attention. */
		tone?: 'error' | 'warning';
		class?: string;
	}

	let { label, value, format, tone, class: klass }: Props = $props();
</script>

<div class={twMerge('flex min-w-0 flex-col gap-1', klass)}>
	<div class="truncate text-[11px] tracking-wide text-muted-content">{label}</div>
	<div
		class={twMerge(
			'text-xl font-semibold tabular-nums tracking-tight leading-none',
			tone === 'error' && value ? 'text-error' : '',
			tone === 'warning' && value ? 'text-warning' : ''
		)}
	>
		{#if value === undefined}
			<span class="text-muted-content">—</span>
		{:else}
			<TweenedMetric target={value} {format} />
		{/if}
	</div>
</div>
