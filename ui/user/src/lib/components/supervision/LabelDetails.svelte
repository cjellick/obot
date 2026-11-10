<script lang="ts">
	import ResponsiveDialog from '$lib/components/ResponsiveDialog.svelte';
	import { m } from '$lib/i18n';
	import { listPolicies, type LabelOrigin, type Policy } from '$lib/services/provenance';
	import { formatTimeAgo } from '$lib/time';
	import { resolve } from '$lib/url';

	// What one label on a session means in context: where the session picked it up, which
	// policies act on it, and the other sessions that carry it.

	let dialog = $state<ReturnType<typeof ResponsiveDialog>>();
	let label = $state('');
	let origins = $state<LabelOrigin[]>([]);
	let policies = $state<Policy[] | undefined>();

	export function show(name: string, from: LabelOrigin[] = []) {
		label = name;
		origins = from;
		policies = undefined;
		dialog?.open();
		listPolicies({ dontLogErrors: true })
			.then((all) => (policies = all.filter((p) => usesLabel(p, name))))
			.catch(() => (policies = []));
	}

	/** Whether a policy's condition tests the label. Conditions name labels as quoted strings. */
	function usesLabel(p: Policy, name: string) {
		return p.condition.includes(`"${name}"`) || p.condition.includes(`'${name}'`);
	}
</script>

<ResponsiveDialog bind:this={dialog} title={label} class="md:w-lg">
	<div class="flex flex-col gap-5 text-sm">
		<section class="flex flex-col gap-2">
			<h3 class="text-xs font-semibold uppercase">{m.supervision_label_details_origin()}</h3>
			{#if origins.length}
				<ul class="flex flex-col gap-1">
					{#each origins.slice(0, 6) as o (o.eventID)}
						<li class="flex justify-between gap-3">
							<span class="break-all">{o.summary || o.eventID}</span>
							<span class="text-muted-content shrink-0 text-xs"
								>{formatTimeAgo(o.at).relativeTime}</span
							>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="text-muted-content">{m.supervision_label_details_no_origin()}</p>
			{/if}
		</section>

		<section class="flex flex-col gap-2">
			<h3 class="text-xs font-semibold uppercase">{m.supervision_label_details_policies()}</h3>
			{#if policies === undefined}
				<p class="text-muted-content animate-pulse">…</p>
			{:else if policies.length}
				<ul class="flex flex-col gap-1">
					{#each policies as p (p.id)}
						<li class="flex items-center justify-between gap-3">
							<a class="link" href={resolve(`/policies/${p.id}`)}>{p.name}</a>
							<span class="text-muted-content text-xs">
								{p.enabled
									? p.mode === 'shadow'
										? m.supervision_label_details_shadow()
										: m.supervision_label_details_enforced()
									: m.supervision_label_details_disabled()}
							</span>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="text-muted-content">{m.supervision_label_details_no_policies()}</p>
			{/if}
		</section>

		<a
			class="btn btn-secondary btn-sm self-start"
			href={resolve(`/sessions?label=${encodeURIComponent(label)}`)}
		>
			{m.supervision_label_details_sessions({ label })}
		</a>
	</div>
</ResponsiveDialog>
