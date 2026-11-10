<script lang="ts">
	import { m } from '$lib/i18n';
	import { resolve } from '$lib/url';
	import type { LoadStatus } from './hub.svelte';
	import { ChevronRight, CircleAlert, TriangleAlert } from '@lucide/svelte';
	import type { Component, Snippet } from 'svelte';
	import { fade } from 'svelte/transition';
	import { twMerge } from 'tailwind-merge';

	export interface HubAttention {
		text: string;
		href: string;
		tone: 'error' | 'warning';
	}

	interface Props {
		id: string;
		title: string;
		description: string;
		icon: Component;
		href: string;
		status: LoadStatus;
		attention?: HubAttention;
		links?: { label: string; href: string }[];
		class?: string;
		children?: Snippet;
	}

	let {
		id,
		title,
		description,
		icon: Icon,
		href,
		status,
		attention,
		links = [],
		class: klass,
		children
	}: Props = $props();

	const titleId = $derived(`hub-card-${id}`);
</script>

{#if status !== 'off' && status !== 'hidden'}
	<section
		class={twMerge('paper hub-card h-full gap-4 p-5', klass)}
		aria-labelledby={titleId}
		data-testid={`hub-card-${id}`}
	>
		<header class="flex items-start gap-3">
			<div
				class="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary"
			>
				<Icon class="size-5" />
			</div>
			<div class="min-w-0 grow">
				<h3 id={titleId} class="text-base font-semibold leading-tight">
					<a
						href={resolve(href)}
						class="group inline-flex items-center gap-1 hover:text-primary transition-colors"
					>
						{title}
						<ChevronRight
							class="size-4 opacity-40 transition-all group-hover:translate-x-0.5 group-hover:opacity-100"
						/>
					</a>
				</h3>
				<p class="text-xs text-muted-content mt-0.5">{description}</p>
			</div>
		</header>

		<div class="flex grow flex-col gap-3">
			{#if status === 'loading'}
				<div class="grid grid-cols-3 gap-3" role="status" aria-label={m.dashboard_hub_loading()}>
					{#each [0, 1, 2] as i (i)}
						<div class="flex flex-col gap-2">
							<div class="skeleton h-3 w-2/3 rounded"></div>
							<div class="skeleton h-6 w-1/2 rounded"></div>
						</div>
					{/each}
				</div>
				<div class="skeleton h-4 w-full rounded"></div>
			{:else if status === 'error'}
				<p class="text-xs text-muted-content">{m.dashboard_hub_unavailable()}</p>
			{:else}
				<div class="flex flex-col gap-3" in:fade={{ duration: 150 }}>
					{@render children?.()}
					{#if attention}
						<a
							href={resolve(attention.href)}
							class={twMerge(
								'flex items-center gap-2 rounded-md px-3 py-2 text-xs font-medium transition-colors',
								attention.tone === 'error'
									? 'bg-error/10 text-error hover:bg-error/15'
									: 'bg-warning/15 text-base-content hover:bg-warning/25'
							)}
						>
							{#if attention.tone === 'error'}
								<CircleAlert class="size-4 shrink-0" />
							{:else}
								<TriangleAlert class="size-4 shrink-0 text-warning" />
							{/if}
							<span class="min-w-0 grow truncate">{attention.text}</span>
							<ChevronRight class="size-3.5 shrink-0" />
						</a>
					{/if}
				</div>
			{/if}
		</div>

		{#if links.length > 0}
			<nav class="flex flex-wrap gap-1.5 border-t border-base-300 pt-3" aria-label={title}>
				{#each links as link (link.href)}
					<a
						href={resolve(link.href)}
						class="rounded-md bg-base-200 px-2 py-1 text-[11px] font-medium text-base-content/80 transition-colors hover:bg-base-300 hover:text-base-content dark:bg-base-300 dark:hover:bg-base-400"
					>
						{link.label}
					</a>
				{/each}
			</nav>
		{/if}
	</section>
{/if}
