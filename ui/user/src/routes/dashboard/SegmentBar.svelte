<script lang="ts">
	import { twMerge } from 'tailwind-merge';

	interface Segment {
		label: string;
		value: number;
		/** Tailwind background class, such as bg-success. */
		color: string;
	}

	interface Props {
		segments: Segment[];
		class?: string;
	}

	let { segments, class: klass }: Props = $props();
	const total = $derived(segments.reduce((sum, s) => sum + s.value, 0));
</script>

{#if total > 0}
	<div class={twMerge('flex flex-col gap-1.5', klass)}>
		<div class="flex h-2 w-full overflow-hidden rounded-full bg-base-300">
			{#each segments as s (s.label)}
				{#if s.value > 0}
					<div class={s.color} style:width={`${(s.value / total) * 100}%`} title={s.label}></div>
				{/if}
			{/each}
		</div>
		<ul class="flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-content">
			{#each segments as s (s.label)}
				<li class="flex items-center gap-1">
					<span class={twMerge('inline-block size-2 rounded-full', s.color)}></span>
					{s.label}
					<span class="tabular-nums text-base-content/80">{s.value.toLocaleString()}</span>
				</li>
			{/each}
		</ul>
	</div>
{/if}
