<script lang="ts">
	import { m } from '$lib/i18n';
	import { goto } from '$lib/url';
	import { MessageCircleQuestionMark, Send, Sparkles } from '@lucide/svelte';

	// Ask about agent activity, optionally about one session. The question opens in Obot AI,
	// which answers it with the session as context and can follow up, act and show live views.

	interface Props {
		sessionId?: string;
		suggestions?: string[];
		intro?: string;
		/** Render without the surrounding card, for embedding in another panel. */
		embedded?: boolean;
		/** Kept for callers; the box no longer grows with answers. */
		scrollable?: boolean;
	}

	let { sessionId, suggestions = [], intro, embedded = false }: Props = $props();

	let question = $state('');

	function ask(q: string) {
		const text = q.trim();
		if (!text) return;
		const session = sessionId ? `&session=${encodeURIComponent(sessionId)}` : '';
		goto(`/ai?ask=${encodeURIComponent(text)}${session}`);
	}

	function onsubmit(e: SubmitEvent) {
		e.preventDefault();
		ask(question);
	}
</script>

{#snippet body()}
	{#if intro}
		<p class="text-muted-content text-sm">{intro}</p>
	{/if}
	{#if suggestions.length}
		<div class="flex flex-wrap gap-2">
			{#each suggestions as s (s)}
				<button
					type="button"
					class="btn btn-secondary btn-xs h-auto min-h-6 py-1 text-left text-xs font-normal"
					onclick={() => ask(s)}
				>
					{s}
				</button>
			{/each}
		</div>
	{/if}

	<form class="flex items-center gap-2" {onsubmit}>
		<label for={`supervisor-ask-${sessionId ?? 'all'}`} class="sr-only">
			{m.supervision_ask_label()}
		</label>
		<input
			id={`supervisor-ask-${sessionId ?? 'all'}`}
			class="text-input-filled flex-1"
			placeholder={m.supervision_ask_placeholder()}
			autocomplete="off"
			bind:value={question}
		/>
		<button
			type="submit"
			class="btn btn-primary btn-square"
			aria-label={m.supervision_ask_submit()}
			disabled={!question.trim()}
		>
			<Send class="size-4" />
		</button>
	</form>
	<p class="text-muted-content flex items-center gap-1 text-xs">
		<Sparkles class="size-3" />
		{m.supervision_ask_opens_ai()}
	</p>
{/snippet}

{#if embedded}
	<div class="flex flex-col gap-3">{@render body()}</div>
{:else}
	<section class="paper gap-3" aria-label={m.supervision_ask_title()}>
		<h2 class="flex items-center gap-2 text-base font-semibold">
			<MessageCircleQuestionMark class="text-primary size-4" />
			{m.supervision_ask_title()}
		</h2>
		{@render body()}
	</section>
{/if}
