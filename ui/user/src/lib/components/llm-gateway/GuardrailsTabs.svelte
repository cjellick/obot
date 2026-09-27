<script lang="ts">
	import { resolve } from '$app/paths';
	import { twMerge } from 'tailwind-merge';

	interface Props {
		active: 'rules' | 'profiles' | 'events';
	}

	let { active }: Props = $props();

	const tabs = [
		{ id: 'rules', label: 'Rules', href: '/llm-gateway/guardrails' },
		{ id: 'profiles', label: 'Profiles', href: '/llm-gateway/guardrails/profiles' },
		{ id: 'events', label: 'Events', href: '/llm-gateway/guardrails/events' }
	] as const;
</script>

<nav class="flex flex-wrap gap-1" aria-label="Guardrails sections">
	{#each tabs as t (t.id)}
		<a
			href={resolve(t.href)}
			aria-current={active === t.id ? 'page' : undefined}
			class={twMerge(
				'rounded-md px-3 py-1 text-xs font-medium transition-colors',
				active === t.id
					? 'bg-primary text-primary-content'
					: 'bg-base-200 dark:bg-base-300 hover:bg-base-300 dark:hover:bg-base-400'
			)}
		>
			{t.label}
		</a>
	{/each}
</nav>
