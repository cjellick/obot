<script lang="ts">
	import { page } from '$app/state';
	import { m } from '$lib/i18n';
	import { resolve } from '$lib/url';
	import { twMerge } from 'tailwind-merge';

	// Labels say what data is; policies say what to do about it. They share one Rules page,
	// with labels first because policies are written in terms of them.
	const tabs = [
		{ href: '/tool-labels', label: m.supervision_labels() },
		{ href: '/policies', label: m.nav_policies() }
	] as const;
</script>

<nav class="mb-4 flex flex-col" aria-label={m.nav_rules()}>
	<div class="flex gap-1">
		{#each tabs as tab (tab.href)}
			{@const selected =
				page.url.pathname === tab.href || page.url.pathname.startsWith(`${tab.href}/`)}
			<a
				href={resolve(tab.href)}
				aria-current={selected ? 'page' : undefined}
				class={twMerge(
					'text-md rounded-t-lg px-8 py-2 font-light text-nowrap',
					selected ? 'bg-primary font-medium text-white' : 'hover:bg-primary/20'
				)}
			>
				{tab.label}
			</a>
		{/each}
	</div>
	<div class="bg-primary h-0.75 w-full shrink-0"></div>
</nav>
