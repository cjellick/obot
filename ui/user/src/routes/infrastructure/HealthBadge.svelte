<script lang="ts">
	import { m } from '$lib/i18n';
	import type { InfraHealth } from '$lib/services/admin/infrastructure';
	import { twMerge } from 'tailwind-merge';

	let { health }: { health: InfraHealth | string } = $props();

	const label = $derived(
		{
			healthy: m.infrastructure_health_healthy(),
			starting: m.infrastructure_health_starting(),
			failing: m.infrastructure_health_failing(),
			stopped: m.infrastructure_health_stopped()
		}[health] ?? health
	);
</script>

<span
	class={twMerge(
		'inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap',
		health === 'healthy' && 'bg-success/10 text-success',
		health === 'starting' && 'bg-warning/10 text-warning',
		health === 'failing' && 'bg-error/10 text-error',
		health === 'stopped' && 'bg-base-300 text-muted-content'
	)}
>
	<span
		class={twMerge(
			'size-1.5 rounded-full',
			health === 'healthy' && 'bg-success',
			health === 'starting' && 'bg-warning',
			health === 'failing' && 'bg-error',
			health === 'stopped' && 'bg-muted-content'
		)}
	></span>
	{label}
</span>
