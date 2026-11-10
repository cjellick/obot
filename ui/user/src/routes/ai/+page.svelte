<script lang="ts">
	import { replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import Confirm from '$lib/components/Confirm.svelte';
	import Layout from '$lib/components/Layout.svelte';
	import AIAnswer from '$lib/components/ai/AIAnswer.svelte';
	import { parseErrorContent } from '$lib/errors';
	import { m } from '$lib/i18n';
	import {
		deleteAIConversation,
		getAIConversation,
		listAIConversations,
		listBlockedEgress,
		streamChatWithAI,
		type AIConversationSummary,
		type AIEvent,
		type AIMessage,
		type AIStep
	} from '$lib/services/ai';
	import { profile } from '$lib/stores';
	import { formatTimeAgo } from '$lib/time';
	import {
		ArrowUp,
		Check,
		ChevronRight,
		History,
		LoaderCircle,
		PanelLeftClose,
		Plus,
		ShieldAlert,
		Sparkles,
		Trash2,
		TriangleAlert,
		Wrench
	} from '@lucide/svelte';
	import { onMount, tick, untrack } from 'svelte';

	// Obot AI. The conversation lives in this tab's session storage so it survives navigating
	// away and back; the server keeps nothing.

	interface Turn extends AIMessage {
		steps?: AIStep[];
		error?: boolean;
	}

	// The answer being streamed: the tools it calls as they run, and its text so far.
	interface LiveStep {
		tool: string;
		arguments?: Record<string, unknown>;
		state: 'running' | 'done' | 'error';
	}
	interface Live {
		steps: LiveStep[];
		text: string;
	}

	const thinkingWords = [
		m.ai_thinking_1,
		m.ai_thinking_2,
		m.ai_thinking_3,
		m.ai_thinking_4,
		m.ai_thinking_5,
		m.ai_thinking_6
	];
	const toolLabels: Record<string, () => string> = {
		llm_usage: m.ai_tool_llm_usage,
		adoption_summary: m.ai_tool_adoption_summary,
		ask_supervisor: m.ai_tool_ask_supervisor,
		list_claude_sessions: m.ai_tool_list_claude_sessions,
		list_agent_sessions: m.ai_tool_list_agent_sessions,
		search_mcp_servers: m.ai_tool_search_mcp_servers,
		list_mcp_server_requests: m.ai_tool_list_mcp_server_requests,
		get_mcp_server_request: m.ai_tool_get_mcp_server_request,
		list_shared_vmcps: m.ai_tool_list_shared_vmcps,
		approve_mcp_server_request: m.ai_tool_approve_mcp_server_request,
		deny_mcp_server_request: m.ai_tool_deny_mcp_server_request,
		request_mcp_server: m.ai_tool_request_mcp_server,
		list_my_mcp_server_requests: m.ai_tool_list_my_mcp_server_requests
	};
	function toolLabel(tool: string) {
		return toolLabels[tool]?.() ?? m.ai_tool_other({ tool });
	}

	const STORAGE_KEY = 'obot-ai-conversation';
	const CONVERSATION_KEY = 'obot-ai-conversation-id';
	const SESSION_KEY = 'obot-ai-session-id';

	let isAdmin = $derived(!!profile.current.isAdmin?.());
	let turns = $state<Turn[]>(untrack(load));
	let input = $state('');
	let busy = $state(false);
	let scroller = $state<HTMLElement>();
	let live = $state<Live>();
	let thinkingIndex = $state(0);
	// The saved conversation on screen, and the caller's other saved conversations.
	let conversationID = $state<string | undefined>(untrack(loadConversationID));
	// The agent session the chat was opened from, which questions are about.
	let contextSessionID = $state<string | undefined>(untrack(() => stored(SESSION_KEY)));
	let history = $state<AIConversationSummary[]>([]);
	// The list of saved chats is closed until the person opens it; their choice is remembered.
	const HISTORY_OPEN_KEY = '@obot/ai/history-open';
	let historyOpen = $state(readHistoryOpen());

	function readHistoryOpen() {
		try {
			return localStorage.getItem(HISTORY_OPEN_KEY) === 'true';
		} catch {
			return false;
		}
	}

	function toggleHistory() {
		historyOpen = !historyOpen;
		try {
			localStorage.setItem(HISTORY_OPEN_KEY, String(historyOpen));
		} catch {
			// Remembering is a convenience.
		}
	}
	let deleting = $state<AIConversationSummary>();
	let deletingBusy = $state(false);

	function loadConversationID(): string | undefined {
		return stored(CONVERSATION_KEY);
	}

	function stored(key: string): string | undefined {
		try {
			return sessionStorage.getItem(key) ?? undefined;
		} catch {
			return undefined;
		}
	}

	function store(key: string, value: string | undefined) {
		try {
			if (value) sessionStorage.setItem(key, value);
			else sessionStorage.removeItem(key);
		} catch {
			// Storage is a convenience only.
		}
	}

	$effect(() => store(CONVERSATION_KEY, conversationID));
	$effect(() => store(SESSION_KEY, contextSessionID));

	async function loadHistory() {
		try {
			history = await listAIConversations({ dontLogErrors: true });
		} catch {
			// The chat works without its history.
		}
	}

	onMount(() => {
		loadHistory();
		// The Dashboard and session pages open Obot AI with a question, and the session it is
		// about, in the URL. Start a new chat with them, then drop them from the URL so a reload
		// doesn't ask again.
		const params = page.url.searchParams;
		const question = params.get('ask')?.trim();
		if (question) {
			newChat();
			contextSessionID = params.get('session') || undefined;
			// Only a convenience; the router may not be ready yet.
			Promise.resolve()
				.then(() => replaceState(page.url.pathname, {}))
				.catch(() => {});
			send(question);
		}
	});

	function newChat() {
		turns = [];
		conversationID = undefined;
		contextSessionID = undefined;
	}

	async function openConversation(id: string) {
		if (busy || id === conversationID) return;
		try {
			const conversation = await getAIConversation(id, { dontLogErrors: true });
			turns = conversation.messages.map(({ role, content, steps }) => ({ role, content, steps }));
			conversationID = conversation.id;
			contextSessionID = undefined;
			scrollToEnd();
		} catch (e) {
			turns = [
				{
					role: 'assistant',
					content: m.ai_error({ error: parseErrorContent(e).message }),
					error: true
				}
			];
			conversationID = undefined;
		}
	}

	async function removeConversation() {
		if (!deleting) return;
		deletingBusy = true;
		try {
			await deleteAIConversation(deleting.id, { dontLogErrors: true });
			if (deleting.id === conversationID) newChat();
			history = history.filter((c) => c.id !== deleting?.id);
			deleting = undefined;
		} finally {
			deletingBusy = false;
		}
	}

	// Rotate the stand-in words while the model works and nothing new is on screen.
	$effect(() => {
		if (!busy) return;
		thinkingIndex = Math.floor(Math.random() * thinkingWords.length);
		const timer = setInterval(() => {
			thinkingIndex = (thinkingIndex + 1) % thinkingWords.length;
		}, 1800);
		return () => clearInterval(timer);
	});

	let waiting = $derived(
		!!live && !live.text && live.steps.every((step) => step.state !== 'running')
	);

	// Hosts Claude environments were blocked from and that are still blocked: Obot AI brings them up.
	let blockedHosts = $state(0);
	$effect(() => {
		if (!isAdmin) return;
		listBlockedEgress(7, { dontLogErrors: true })
			.then((items) => (blockedHosts = items.filter((b) => !b.allowedNow?.length).length))
			.catch(() => (blockedHosts = 0));
	});

	let suggestions = $derived(
		isAdmin
			? [
					m.ai_suggestion_briefing(),
					m.ai_suggestion_cost(),
					m.ai_suggestion_security(),
					m.ai_suggestion_requests()
				]
			: [m.ai_suggestion_find(), m.ai_suggestion_my_requests()]
	);

	function load(): Turn[] {
		try {
			return JSON.parse(sessionStorage.getItem(STORAGE_KEY) ?? '[]') as Turn[];
		} catch {
			return [];
		}
	}

	$effect(() => {
		const snapshot = JSON.stringify(turns);
		try {
			sessionStorage.setItem(STORAGE_KEY, snapshot);
		} catch {
			// Storage is a convenience only.
		}
	});

	// The page scrolls in the layout's main column, not in the conversation. Keep it pinned to
	// the bottom while an answer streams and widgets load, unless the reader scrolled up.
	const STICK_THRESHOLD_PX = 80;
	let pinned = true;

	function scrollParent(el: HTMLElement | undefined): HTMLElement | undefined {
		for (let p = el?.parentElement; p; p = p.parentElement) {
			const overflow = getComputedStyle(p).overflowY;
			if (overflow === 'auto' || overflow === 'scroll') return p;
		}
		return undefined;
	}

	function toBottom(behavior: 'auto' | 'smooth' = 'auto') {
		const parent = scrollParent(scroller);
		parent?.scrollTo({ top: parent.scrollHeight, behavior });
	}

	$effect(() => {
		const parent = scrollParent(scroller);
		if (!parent || !scroller) return;
		const onScroll = () => {
			pinned = parent.scrollHeight - parent.scrollTop - parent.clientHeight < STICK_THRESHOLD_PX;
		};
		const observer = new ResizeObserver(() => {
			if (pinned) toBottom();
		});
		observer.observe(scroller);
		parent.addEventListener('scroll', onScroll, { passive: true });
		return () => {
			observer.disconnect();
			parent.removeEventListener('scroll', onScroll);
		};
	});

	/** Scrolls to the end now, for the reader's own actions such as sending a question. */
	async function scrollToEnd() {
		await tick();
		pinned = true;
		toBottom('smooth');
	}

	function onEvent(event: AIEvent) {
		if (!live) return;
		switch (event.type) {
			case 'thinking':
				// A new model turn: text from an earlier turn was a preamble to tool calls.
				live.text = '';
				break;
			case 'tool_start':
				live.text = '';
				live.steps.push({ tool: event.tool, arguments: event.arguments, state: 'running' });
				break;
			case 'tool_end': {
				const step = live.steps.findLast((s) => s.tool === event.tool && s.state === 'running');
				if (step) step.state = event.isError ? 'error' : 'done';
				break;
			}
			case 'delta':
				live.text += event.text;
				break;
		}
	}

	async function send(text = input) {
		const question = text.trim();
		if (!question || busy) return;
		input = '';
		turns = [...turns, { role: 'user', content: question }];
		busy = true;
		live = { steps: [], text: '' };
		scrollToEnd();
		try {
			const messages: AIMessage[] = turns
				.filter((t) => !t.error)
				.map(({ role, content }) => ({ role, content }));
			const reply = await streamChatWithAI(messages, onEvent, {
				conversationID,
				sessionID: contextSessionID
			});
			turns = [...turns, { role: 'assistant', content: reply.reply, steps: reply.steps }];
			if (reply.conversationID) conversationID = reply.conversationID;
			loadHistory();
		} catch (e) {
			turns = [
				...turns,
				{
					role: 'assistant',
					content: m.ai_error({ error: parseErrorContent(e).message }),
					error: true
				}
			];
		} finally {
			busy = false;
			live = undefined;
		}
	}

	function onKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
			e.preventDefault();
			send();
		}
	}

	function formatArguments(args?: Record<string, unknown>) {
		if (!args || Object.keys(args).length === 0) return '';
		return JSON.stringify(args);
	}
</script>

<svelte:head>
	<title>Obot | {m.ai_title()}</title>
</svelte:head>

<Layout title={m.ai_title()} rightNavActions={navActions}>
	<div class="flex min-h-[calc(100svh-9rem)] gap-6">
		{#if history.length > 0 && historyOpen}
			<nav
				class="bg-base-100 dark:bg-base-200 border-base-300 dark:border-base-400 sticky top-28 flex max-h-[calc(100svh-8rem)] w-64 shrink-0 flex-col gap-1 self-start overflow-y-auto rounded-xl border p-2 shadow-sm"
				aria-label={m.ai_history()}
			>
				<div class="flex items-center justify-between px-2 pb-1">
					<h2 class="text-muted-content text-xs font-semibold uppercase">
						{m.ai_history()}
					</h2>
					<button
						type="button"
						class="text-muted-content hover:text-base-content p-1"
						aria-label={m.ai_history_close()}
						onclick={toggleHistory}
					>
						<PanelLeftClose class="size-4" />
					</button>
				</div>
				<ul class="flex flex-col gap-0.5">
					{#each history as conversation (conversation.id)}
						<li
							class="group flex items-center gap-1 rounded-md {conversation.id === conversationID
								? 'bg-primary/10'
								: 'hover:bg-base-200 dark:hover:bg-base-300'}"
						>
							<button
								type="button"
								class="flex min-w-0 flex-1 flex-col items-start px-2 py-1.5 text-left"
								aria-current={conversation.id === conversationID ? 'true' : undefined}
								disabled={busy}
								onclick={() => openConversation(conversation.id)}
							>
								<span class="w-full truncate text-sm">{conversation.title}</span>
								<span class="text-muted-content text-xs">
									{formatTimeAgo(conversation.updatedAt).relativeTime}
								</span>
							</button>
							<button
								type="button"
								class="text-muted-content hover:text-error p-1.5 opacity-0 group-hover:opacity-100 focus:opacity-100"
								aria-label={m.ai_delete_chat({ title: conversation.title })}
								onclick={() => (deleting = conversation)}
							>
								<Trash2 class="size-3.5" />
							</button>
						</li>
					{/each}
				</ul>
			</nav>
		{/if}
		<div class="flex min-w-0 flex-1 flex-col gap-4">
			<div bind:this={scroller} class="flex flex-1 flex-col gap-4 pb-2" aria-live="polite">
				{#if turns.length === 0}
					<div class="mt-8 flex flex-col items-center gap-4 text-center">
						<Sparkles class="text-primary size-12" />
						<p class="text-muted-content max-w-xl text-sm">
							{isAdmin ? m.ai_intro_admin() : m.ai_intro_user()}
						</p>
						{#if blockedHosts > 0}
							<button
								type="button"
								class="notification-alert flex items-center gap-2 rounded-xl px-4 py-2 text-left text-sm"
								onclick={() => send(m.ai_suggestion_blocked_egress())}
							>
								<ShieldAlert class="size-4 shrink-0" />
								{m.ai_attention_blocked_egress({ count: blockedHosts })}
							</button>
						{/if}
						<div class="flex max-w-2xl flex-wrap justify-center gap-2">
							{#each suggestions as suggestion (suggestion)}
								<button
									type="button"
									class="btn btn-secondary rounded-4xl text-left text-sm"
									onclick={() => send(suggestion)}
								>
									{suggestion}
								</button>
							{/each}
						</div>
					</div>
				{/if}

				{#each turns as turn, i (i)}
					{#if turn.role === 'user'}
						<div class="bg-primary/10 self-end rounded-2xl px-4 py-2 text-sm whitespace-pre-wrap">
							{turn.content}
						</div>
					{:else}
						<article
							class="dark:bg-base-200 bg-base-100 flex max-w-full flex-col gap-2 self-start rounded-2xl p-4 text-sm shadow-sm {turn.error
								? 'notification-error'
								: ''}"
						>
							{#if turn.steps?.length}
								<details class="text-muted-content text-xs">
									<summary class="flex cursor-pointer items-center gap-1">
										<Wrench class="size-3" />
										{m.ai_steps({ count: turn.steps.length })}
										<ChevronRight class="size-3" />
									</summary>
									<ul class="mt-2 flex flex-col gap-1">
										{#each turn.steps as step, j (j)}
											<li class="font-mono break-all">
												{step.tool}{formatArguments(step.arguments)}
												{#if step.isError}<span class="text-red-500">
														({m.ai_step_failed()})</span
													>{/if}
											</li>
										{/each}
									</ul>
								</details>
							{/if}
							{#if turn.error}
								<p>{turn.content}</p>
							{:else}
								<AIAnswer content={turn.content} />
							{/if}
						</article>
					{/if}
				{/each}

				{#if busy && live}
					<article
						class="dark:bg-base-200 bg-base-100 flex max-w-full flex-col gap-2 self-start rounded-2xl p-4 text-sm shadow-sm"
						aria-busy="true"
					>
						{#if live.steps.length}
							<ul
								class="text-muted-content flex flex-col gap-1 text-xs"
								aria-label={m.ai_steps({ count: live.steps.length })}
							>
								{#each live.steps as step, j (j)}
									<li class="flex items-center gap-2">
										{#if step.state === 'running'}
											<LoaderCircle class="text-primary size-3.5 animate-spin" />
										{:else if step.state === 'error'}
											<TriangleAlert class="size-3.5 text-red-500" />
										{:else}
											<Check class="size-3.5 text-success" />
										{/if}
										<span>{toolLabel(step.tool)}</span>
										<span class="sr-only">
											{step.state === 'running'
												? m.ai_step_running()
												: step.state === 'error'
													? m.ai_step_failed()
													: m.ai_step_done()}
										</span>
									</li>
								{/each}
							</ul>
						{/if}
						{#if live.text}
							<AIAnswer content={live.text} />
						{:else if waiting}
							<p class="text-muted-content animate-pulse" role="status">
								{thinkingWords[thinkingIndex]()}
							</p>
						{/if}
					</article>
				{/if}
			</div>

			<form
				class="dark:bg-base-200 bg-base-100 sticky bottom-4 z-10 mb-4 flex items-end gap-2 rounded-2xl p-2 shadow-md"
				onsubmit={(e) => {
					e.preventDefault();
					send();
				}}
			>
				<textarea
					class="max-h-48 min-h-10 flex-1 resize-none bg-transparent px-2 py-2 text-sm outline-none"
					placeholder={m.ai_placeholder()}
					aria-label={m.ai_placeholder()}
					rows="1"
					bind:value={input}
					onkeydown={onKeydown}></textarea>
				<button
					type="submit"
					class="btn btn-primary size-10 rounded-full p-0"
					aria-label={m.ai_send()}
					disabled={busy || !input.trim()}
				>
					<ArrowUp class="size-4" />
				</button>
			</form>
		</div>
	</div>
</Layout>

<Confirm
	show={!!deleting}
	msg={m.ai_delete_chat_confirm({ title: deleting?.title ?? '' })}
	submitText={m.ai_delete()}
	loading={deletingBusy}
	onsuccess={removeConversation}
	oncancel={() => (deleting = undefined)}
/>

{#snippet navActions()}
	{#if history.length > 0}
		<button
			type="button"
			class="btn btn-secondary rounded-4xl text-sm"
			aria-pressed={historyOpen}
			onclick={toggleHistory}
		>
			<History class="size-4" />
			{historyOpen ? m.ai_history_hide() : m.ai_history_show({ count: history.length })}
		</button>
	{/if}
	{#if turns.length > 0}
		<button
			type="button"
			class="btn btn-secondary rounded-4xl text-sm"
			disabled={busy}
			onclick={newChat}
		>
			<Plus class="size-4" />
			{m.ai_new_chat()}
		</button>
	{/if}
{/snippet}
