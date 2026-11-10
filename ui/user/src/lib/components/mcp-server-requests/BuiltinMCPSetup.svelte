<script lang="ts">
	import CopyButton from '$lib/components/CopyButton.svelte';
	import { m } from '$lib/i18n';
	import {
		BUILTIN_ADMIN_MCP_ID,
		BUILTIN_USER_MCP_ID,
		builtinMCPURL,
		clientSnippets,
		cursorInstallLink,
		managedSettingsSnippets,
		type ClientSnippet
	} from '$lib/services/mcpserverrequests';
	import {
		ChevronRight,
		Code,
		MousePointerClick,
		Plug,
		ShieldCheck,
		SquareTerminal,
		Terminal,
		Users
	} from '@lucide/svelte';

	// How to connect an agent to Obot's built-in MCP servers, and for administrators, the
	// managed settings that push the user server to everyone.
	let { admin = false }: { admin?: boolean } = $props();

	let origin = $derived(typeof window === 'undefined' ? '' : window.location.origin);
	let userURL = $derived(builtinMCPURL(BUILTIN_USER_MCP_ID, origin));
	let servers = $derived([
		{
			id: 'user',
			icon: Users,
			label: m.server_requests_user_server(),
			hint: m.server_requests_user_server_hint(),
			url: userURL
		},
		...(admin
			? [
					{
						id: 'admin',
						icon: ShieldCheck,
						label: m.server_requests_admin_server(),
						hint: m.server_requests_admin_server_hint(),
						url: builtinMCPURL(BUILTIN_ADMIN_MCP_ID, origin)
					}
				]
			: [])
	]);

	let snippets = $derived(clientSnippets(userURL));
	let managed = $derived(admin ? managedSettingsSnippets(origin) : []);

	type Client = {
		id: string;
		name: string;
		icon: typeof Plug;
		hint: string;
		snippets: ClientSnippet[];
		installLink?: { href: string; label: string };
		deploy?: { snippets: ClientSnippet[]; note: string };
	};

	let clients = $derived<Client[]>([
		{
			id: 'claude-code',
			name: 'Claude Code',
			icon: SquareTerminal,
			hint: m.server_requests_client_claude_hint(),
			snippets: snippets.claude,
			deploy: admin
				? {
						snippets: managed.filter((s) => s.id !== 'codex-managed-config'),
						note: m.server_requests_deploy_claude_hint()
					}
				: undefined
		},
		{
			id: 'codex',
			name: 'Codex',
			icon: Terminal,
			hint: m.server_requests_client_codex_hint(),
			snippets: snippets.codex,
			deploy: admin
				? {
						snippets: managed.filter((s) => s.id === 'codex-managed-config'),
						note: m.server_requests_setup_codex_hint()
					}
				: undefined
		},
		{
			id: 'vscode',
			name: 'VS Code / GitHub Copilot',
			icon: Code,
			hint: m.server_requests_client_vscode_hint(),
			snippets: snippets.vscode
		},
		{
			id: 'cursor',
			name: 'Cursor',
			icon: MousePointerClick,
			hint: m.server_requests_client_cursor_hint(),
			snippets: snippets.cursor,
			installLink: { href: cursorInstallLink(userURL), label: m.server_requests_cursor_add() }
		}
	]);
</script>

{#snippet codeBlock(snippet: ClientSnippet)}
	<div class="flex min-w-0 flex-col gap-1">
		<div class="flex items-center justify-between gap-2">
			<span class="text-muted-content truncate text-xs font-medium"
				>{snippet.label ?? m.server_requests_terminal()}</span
			>
			<CopyButton text={snippet.value} />
		</div>
		<pre
			class="bg-base-200 dark:bg-base-300 overflow-x-auto rounded-md p-3 text-xs leading-relaxed">{snippet.value}</pre>
	</div>
{/snippet}

<section class="flex flex-col gap-6" aria-labelledby="builtin-mcp-setup-title">
	<div
		class="border-primary/20 from-primary/10 via-base-100 to-base-100 dark:from-primary/20 dark:via-base-200 dark:to-base-200 flex flex-col gap-5 rounded-xl border bg-linear-to-br p-6 shadow-sm"
	>
		<div class="flex items-start gap-4">
			<div
				class="bg-primary/15 text-primary flex size-11 shrink-0 items-center justify-center rounded-xl"
			>
				<Plug class="size-6" />
			</div>
			<div class="flex flex-col gap-1">
				<h2 id="builtin-mcp-setup-title" class="text-xl font-semibold">
					{m.server_requests_setup_title()}
				</h2>
				<p class="text-muted-content text-sm">{m.server_requests_setup_description()}</p>
			</div>
		</div>

		<div class="grid gap-4 {servers.length > 1 ? 'lg:grid-cols-2' : ''}">
			{#each servers as server (server.id)}
				{@const ServerIcon = server.icon}
				<div
					class="bg-base-100 dark:bg-base-300 dark:border-base-400 border-base-300 flex min-w-0 flex-col gap-3 rounded-lg border p-4"
					data-testid="builtin-server-{server.id}"
				>
					<div class="flex items-center gap-2">
						<ServerIcon class="text-primary size-4 shrink-0" />
						<h3 class="font-semibold">{server.label}</h3>
					</div>
					<p class="text-muted-content text-sm">{server.hint}</p>
					<div class="flex min-w-0 items-center gap-2">
						<code
							class="bg-base-200 dark:bg-base-200 min-w-0 flex-1 truncate rounded-md px-3 py-2 font-mono text-sm md:text-base"
							title={server.url}>{server.url}</code
						>
						<CopyButton
							text={server.url}
							buttonText={m.core_copy()}
							classes={{ button: 'shrink-0' }}
						/>
					</div>
				</div>
			{/each}
		</div>
	</div>

	<div class="flex flex-col gap-1">
		<h2 class="text-lg font-semibold">{m.server_requests_setup_commands()}</h2>
		<p class="text-muted-content text-sm">{m.server_requests_clients_description()}</p>
	</div>

	<div class="grid gap-4 md:grid-cols-2">
		{#each clients as client (client.id)}
			{@const ClientIcon = client.icon}
			<article class="paper min-w-0" aria-labelledby="client-{client.id}-title">
				<div class="flex items-center gap-3">
					<div
						class="bg-primary/10 text-primary flex size-9 shrink-0 items-center justify-center rounded-lg"
					>
						<ClientIcon class="size-5" />
					</div>
					<h3 id="client-{client.id}-title" class="font-semibold">{client.name}</h3>
				</div>
				<p class="text-muted-content text-sm">{client.hint}</p>

				{#if client.installLink}
					<a href={client.installLink.href} class="btn btn-primary w-fit text-sm">
						{client.installLink.label}
					</a>
				{/if}

				{#each client.snippets as snippet (snippet.id)}
					{@render codeBlock(snippet)}
				{/each}

				{#if client.deploy && client.deploy.snippets.length > 0}
					<details class="group border-base-300 dark:border-base-400 border-t pt-3">
						<summary
							class="flex cursor-pointer list-none items-center gap-1 text-sm font-medium [&::-webkit-details-marker]:hidden"
						>
							<ChevronRight class="size-4 transition-transform group-open:rotate-90" />
							{m.server_requests_deploy_title()}
						</summary>
						<div class="mt-3 flex flex-col gap-3">
							<p class="text-muted-content text-xs">{client.deploy.note}</p>
							{#each client.deploy.snippets as snippet (snippet.id)}
								{@render codeBlock(snippet)}
							{/each}
						</div>
					</details>
				{/if}
			</article>
		{/each}
	</div>
</section>
