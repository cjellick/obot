<script lang="ts">
	import { twMerge } from 'tailwind-merge';

	interface Props {
		values: number[];
		class?: string;
		label?: string;
	}

	let { values, class: klass, label }: Props = $props();

	const points = $derived.by(() => {
		if (values.length < 2) return '';
		const max = Math.max(...values, 1);
		const step = 100 / (values.length - 1);
		return values
			.map((v, i) => `${(i * step).toFixed(2)},${(28 - (v / max) * 26).toFixed(2)}`)
			.join(' ');
	});
</script>

{#if points}
	<svg
		viewBox="0 0 100 30"
		preserveAspectRatio="none"
		class={twMerge('h-10 w-full text-primary', klass)}
		role="img"
		aria-label={label}
	>
		<polygon points={`0,30 ${points} 100,30`} fill="currentColor" opacity="0.12" />
		<polyline
			{points}
			fill="none"
			stroke="currentColor"
			stroke-width="1.5"
			stroke-linejoin="round"
			stroke-linecap="round"
			vector-effect="non-scaling-stroke"
		/>
	</svg>
{/if}
