<script lang="ts">
	import Navbar from '$lib/components/Navbar.svelte';
	import {
		Sparkles,
		ShieldAlert,
		Check,
		Loader2,
		Database,
		Users,
		FileCode,
		Laptop,
		KeyRound,
		Activity,
		ScrollText,
		ArrowRight
	} from '@lucide/svelte';
	import { onMount, tick } from 'svelte';
	import { fly, fade } from 'svelte/transition';

	type Choice = { label: string; value: string };
	type Msg = {
		role: 'ai' | 'user';
		text: string;
		choices?: Choice[];
		multi?: boolean;
		answered?: boolean;
		kind?: 'sources' | 'collect' | 'policies';
	};

	type Source = {
		key: string;
		name: string;
		why: string;
		icon: typeof Database;
		status: 'idle' | 'connecting' | 'connected';
		found?: string;
	};
	type Risk = {
		title: string;
		severity: 'critical' | 'high' | 'medium' | 'low';
		segment: string;
		framework: string;
		evidence: string;
		policy: string;
	};

	let messages = $state<Msg[]>([]);
	let thinking = $state(false);
	let selected = $state<string[]>([]);
	let chatEl: HTMLDivElement | undefined = $state();

	// profile being built
	let industry = $state('');
	let frameworks = $state<string[]>([]);
	let tools = $state<string[]>([]);
	let segments = $state<string[]>([]);
	let idp = $state('');
	let code = $state('');
	let sources = $state<Source[]>([]);
	let risks = $state<Risk[]>([]);
	let collecting = $state(false);
	let policiesShown = $state(false);

	const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

	async function scroll() {
		await tick();
		chatEl?.scrollTo({ top: chatEl.scrollHeight, behavior: 'smooth' });
	}

	async function ai(text: string, extra: Partial<Msg> = {}, delay = 900) {
		thinking = true;
		await scroll();
		await sleep(delay);
		thinking = false;
		messages.push({ role: 'ai', text, ...extra });
		await scroll();
	}

	function user(text: string) {
		messages.push({ role: 'user', text });
		scroll();
	}

	let step = 0;

	async function answer(msg: Msg, values: string[]) {
		msg.answered = true;
		const labels = msg.choices!.filter((c) => values.includes(c.value)).map((c) => c.label);
		user(labels.join(', '));
		selected = [];
		step++;
		await flow[step](values);
	}

	const flow: Record<number, (v: string[]) => Promise<void>> = {
		0: async () => {
			await ai(
				"Hi, I'm going to help you stand up an AI risk register. I'll ask a few questions to figure out which risks matter most to you and where we should pull data from. Takes about two minutes.",
				{},
				600
			);
			await ai('First: what industry are you in?', {
				choices: [
					{ label: 'Healthcare', value: 'healthcare' },
					{ label: 'Financial services', value: 'finance' },
					{ label: 'Software / SaaS', value: 'saas' },
					{ label: 'Public sector', value: 'public' }
				]
			});
		},
		1: async ([v]) => {
			industry = v;
			if (v === 'healthcare') {
				await ai(
					'Got it. Healthcare usually means HIPAA. Does your organization create, receive, or store protected health information (PHI)?',
					{
						choices: [
							{ label: "Yes, we're a covered entity", value: 'ce' },
							{ label: "Yes, we're a business associate", value: 'ba' },
							{ label: 'Not sure', value: 'unsure' }
						]
					}
				);
			} else if (v === 'finance') {
				await ai('Are you subject to any of these?', {
					multi: true,
					choices: [
						{ label: 'SOX', value: 'SOX' },
						{ label: 'GLBA', value: 'GLBA' },
						{ label: 'PCI DSS', value: 'PCI DSS' },
						{ label: 'NYDFS Part 500', value: 'NYDFS 500' }
					]
				});
			} else {
				await ai('Which compliance frameworks are you held to today?', {
					multi: true,
					choices: [
						{ label: 'SOC 2', value: 'SOC 2' },
						{ label: 'ISO 27001', value: 'ISO 27001' },
						{ label: 'GDPR', value: 'GDPR' },
						{ label: 'EU AI Act', value: 'EU AI Act' }
					]
				});
			}
		},
		2: async (v) => {
			if (industry === 'healthcare') {
				frameworks = ['HIPAA Security Rule', 'HIPAA Privacy Rule', 'NIST AI RMF'];
				await ai(
					"That puts **HIPAA** squarely in scope. The big AI questions for you will be: is PHI going into models that aren't covered by a BAA, and can you produce an audit trail of who accessed what. I'm also mapping to the **NIST AI RMF** so you have a general AI baseline."
				);
			} else {
				frameworks = [...v, 'NIST AI RMF'];
				await ai(
					`Noted. I'll map risks to ${v.join(', ')} plus the **NIST AI RMF** as a general AI baseline.`
				);
			}
			await ai(
				'Which AI tools do you know people are using today? Pick all that apply, even unofficial ones.',
				{
					multi: true,
					choices: [
						{ label: 'Claude Code', value: 'Claude Code' },
						{ label: 'Cursor', value: 'Cursor' },
						{ label: 'GitHub Copilot', value: 'GitHub Copilot' },
						{ label: 'Claude.ai / Claude Enterprise', value: 'Claude Enterprise' },
						{ label: 'ChatGPT', value: 'ChatGPT' },
						{ label: 'Internal apps on model APIs', value: 'API apps' },
						{ label: 'Honestly, no idea', value: 'unknown' }
					]
				}
			);
		},
		3: async (v) => {
			tools = v.filter((t) => t !== 'unknown');
			const s = new Set<string>(['Platform']);
			if (v.some((t) => ['Claude Code', 'Cursor', 'GitHub Copilot'].includes(t)))
				s.add('Coding agents');
			if (v.some((t) => ['Claude Enterprise', 'ChatGPT'].includes(t))) s.add('Business users');
			if (v.includes('API apps')) s.add('API workloads');
			if (v.includes('unknown')) {
				s.add('Coding agents');
				s.add('Business users');
			}
			segments = [...s];
			await ai(
				v.includes('unknown')
					? "That's the most common answer, and it's fine. Discovering what's actually in use is the first job of the register. I'll plan for both coding agents and business users."
					: `So you have usage across **${segments.filter((x) => x !== 'Platform').join(', ')}**. Those have very different risk shapes: coding agents run tools on laptops with repo and shell access, business users paste data into chat.`
			);
			await ai('Where do your employee identities live?', {
				choices: [
					{ label: 'Okta', value: 'Okta' },
					{ label: 'Microsoft Entra ID', value: 'Entra ID' },
					{ label: 'Google Workspace', value: 'Google Workspace' }
				]
			});
		},
		4: async ([v]) => {
			idp = v;
			await ai(
				`${v} is a great starting point. App assignments there will tell us which AI apps people actually have access to, including ones nobody told security about.`
			);
			await ai('And where does your code live?', {
				choices: [
					{ label: 'GitHub', value: 'GitHub' },
					{ label: 'GitLab', value: 'GitLab' },
					{ label: 'Bitbucket', value: 'Bitbucket' }
				]
			});
		},
		5: async ([v]) => {
			code = v;
			buildSources();
			await ai(
				"Here's where I'd start, in order of how much signal we get for the effort. You can connect these now or skip any and come back later.",
				{ kind: 'sources' },
				1400
			);
		}
	};

	function buildSources() {
		const list: Source[] = [
			{
				key: 'obot',
				name: 'Obot',
				why: 'MCP servers, vMCPs, guardrail findings, LLM gateway traffic, audit retention',
				icon: Activity,
				status: 'connected',
				found: '14 MCP servers · 212 users'
			},
			{
				key: 'idp',
				name: idp,
				why: 'Which AI apps are assigned to whom. Finds shadow AI.',
				icon: Users,
				status: 'idle'
			}
		];
		if (segments.includes('Business users') || tools.includes('Claude Enterprise'))
			list.push({
				key: 'compliance',
				name: 'Claude Compliance API',
				why: 'Activity feed for Claude Enterprise: uploads, connectors, sharing',
				icon: ScrollText,
				status: 'idle'
			});
		if (segments.includes('API workloads') || tools.length === 0)
			list.push({
				key: 'admin',
				name: 'Anthropic Admin API',
				why: 'API keys, who owns them, and whether workspaces have retention set',
				icon: KeyRound,
				status: 'idle'
			});
		list.push({
			key: 'repos',
			name: `${code} repositories`,
			why: 'Agent configs checked into repos: MCP servers, CLAUDE.md, permission allowlists',
			icon: FileCode,
			status: 'idle'
		});
		if (segments.includes('Coding agents'))
			list.push({
				key: 'sentry',
				name: 'Obot Sentry (devices)',
				why: 'What agents and local MCP servers are actually on laptops',
				icon: Laptop,
				status: 'idle'
			});
		sources = list;
	}

	const foundText: Record<string, () => string> = {
		idp: () => '11 AI apps · 3 not approved',
		compliance: () => '48,210 events (30 days)',
		admin: () => '37 API keys · 9 unowned',
		repos: () => '612 repos · 41 with agent config',
		sentry: () => '188 devices · 23 local MCP servers'
	};

	async function connect(s: Source) {
		s.status = 'connecting';
		await sleep(900 + Math.random() * 900);
		s.status = 'connected';
		s.found = foundText[s.key]?.();
	}

	async function connectAll() {
		await Promise.all(sources.filter((s) => s.status === 'idle').map(connect));
	}

	let allConnected = $derived(sources.length > 0 && sources.every((s) => s.status === 'connected'));
	let collectStarted = $state(false);

	function riskCatalog(): Risk[] {
		const hipaa = industry === 'healthcare';
		const out: Risk[] = [];
		out.push({
			title: hipaa
				? 'PHI may be sent to models not covered by a BAA'
				: 'Sensitive data sent to unapproved model providers',
			severity: 'critical',
			segment: 'Business users',
			framework: hipaa ? 'HIPAA §164.502(e), §164.308(b)' : 'NIST AI RMF MAP 4.1',
			evidence: `${idp}: ChatGPT (personal) assigned to 0 users but seen in 23 browser sessions; 2 unapproved AI apps`,
			policy:
				'Route model traffic through the Obot LLM gateway; block providers without a signed BAA'
		});
		if (segments.includes('Coding agents'))
			out.push({
				title: 'Coding agents run unvetted local MCP servers',
				severity: 'high',
				segment: 'Coding agents',
				framework: hipaa ? 'HIPAA §164.308(a)(1)' : 'NIST AI RMF MANAGE 2.2',
				evidence: 'Sentry: 23 local MCP servers on 61 devices, 9 not in the Obot catalog',
				policy: 'Allow only catalog MCP servers on managed devices (Sentry enforcement)'
			});
		out.push({
			title: 'Agent configs in repos grant broad shell and file access',
			severity: 'high',
			segment: 'Coding agents',
			framework: 'NIST AI RMF GOVERN 1.4',
			evidence: `${code}: 17 repos allow Bash(*) in .claude/settings.json; 4 reference personal MCP endpoints`,
			policy: 'Require a reviewed agent permission profile; flag wildcard allowlists in CI'
		});
		out.push({
			title: 'Anthropic API keys with no owner or retention setting',
			severity: 'medium',
			segment: 'API workloads',
			framework: hipaa ? 'HIPAA §164.312(a)(1)' : 'SOC 2 CC6.1',
			evidence: 'Admin API: 9 of 37 keys have no owner; 3 not used in 90 days',
			policy: 'Issue model access through Obot virtual keys tied to a named owner'
		});
		out.push({
			title: 'AI audit records deleted after 90 days and not exported',
			severity: hipaa ? 'high' : 'medium',
			segment: 'Platform',
			framework: hipaa ? 'HIPAA §164.316(b)(2) (6-year retention)' : 'SOC 2 CC7.2',
			evidence: 'Obot: MCP and LLM audit retention = 90 days; no SIEM export configured',
			policy: 'Export audit logs to SIEM; extend retention to 6 years'
		});
		if (segments.includes('Business users'))
			out.push({
				title: 'Claude connectors share files outside the org',
				severity: 'medium',
				segment: 'Business users',
				framework: hipaa ? 'HIPAA §164.312(e)(1)' : 'NIST AI RMF MEASURE 2.10',
				evidence:
					'Compliance API: 14 projects shared publicly in 30 days; 3 Google Drive connectors',
				policy: 'Disable public project sharing; allowlist connectors'
			});
		out.push({
			title: 'Hosted MCP servers have unrestricted network egress',
			severity: 'low',
			segment: 'Platform',
			framework: 'NIST AI RMF MANAGE 4.1',
			evidence: 'Obot: 6 of 14 hosted servers have no egress policy',
			policy: 'Enable default-deny egress for hosted MCP servers'
		});
		return out;
	}

	async function startCollect() {
		collectStarted = true;
		collecting = true;
		await ai(
			"Running the first collection now. I'm pulling from each source, running the fixed checks, and mapping anything I find to your frameworks.",
			{ kind: 'collect' },
			500
		);
		for (const r of riskCatalog()) {
			await sleep(700);
			risks.push(r);
		}
		collecting = false;
		const crit = risks.filter((r) => r.severity === 'critical' || r.severity === 'high').length;
		await ai(
			`Done. Your register has **${risks.length} open risks**, ${crit} of them high or critical. Every one links back to the records that produced it. Each also has a proposed Obot policy. Want me to draft those?`,
			{
				choices: [
					{ label: 'Draft the policies', value: 'yes' },
					{ label: "I'll review the risks first", value: 'later' }
				]
			}
		);
		step = 99;
	}

	async function policies(msg: Msg, v: string) {
		msg.answered = true;
		user(v === 'yes' ? 'Draft the policies' : "I'll review the risks first");
		if (v === 'yes') {
			policiesShown = true;
			await ai(
				`Here are ${risks.length} draft policies, one per risk. They start in **monitor** mode so you can see what they'd catch before anything gets blocked. Once you enforce one, the risk moves to *treated* and the register keeps collecting evidence that it stays that way.`,
				{ kind: 'policies' },
				1500
			);
		} else {
			await ai(
				"Sounds good. The register is on the right. I'll re-collect every 24 hours and flag anything new. When you're ready, ask me to draft policies."
			);
		}
	}

	const sevStyle: Record<Risk['severity'], string> = {
		critical: 'background:#dc2626;color:#fff',
		high: 'background:#ea580c;color:#fff',
		medium: 'background:#fcd34d;color:#451a03',
		low: 'background:#d1d5db;color:#1f2937'
	};
	const tag = {
		fw: 'background:rgba(147,51,234,.14);color:#7e22ce',
		seg: 'background:rgba(14,165,233,.14);color:#0369a1',
		pol: 'background:rgba(37,99,235,.14);color:#1d4ed8'
	};

	function md(s: string) {
		return s.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>').replace(/\*(.+?)\*/g, '<em>$1</em>');
	}

	onMount(() => {
		flow[0]([]);
	});
</script>

<svelte:head>
	<title>Obot | AI Risk Register</title>
</svelte:head>

<div class="flex h-dvh flex-col bg-gray-50 dark:bg-black">
	<Navbar class="dark:bg-gray-990 sticky top-0 left-0 z-30 w-full" unauthorized />

	<div class="flex min-h-0 grow">
		<!-- Chat -->
		<div class="flex min-w-0 flex-1 flex-col border-r border-gray-200 dark:border-gray-800">
			<div class="flex items-center gap-2 border-b border-gray-200 px-6 py-3 dark:border-gray-800">
				<Sparkles class="text-primary size-5" />
				<h1 class="text-lg font-semibold">Set up your AI risk register</h1>
			</div>
			<div bind:this={chatEl} class="flex grow flex-col gap-4 overflow-y-auto px-6 py-6">
				{#each messages as msg, i (i)}
					<div
						in:fly={{ y: 8, duration: 250 }}
						class={msg.role === 'user' ? 'flex justify-end' : 'flex justify-start gap-3'}
					>
						{#if msg.role === 'ai'}
							<div
								class="bg-primary/10 text-primary flex size-8 shrink-0 items-center justify-center rounded-full"
							>
								<Sparkles class="size-4" />
							</div>
						{/if}
						<div class="flex max-w-[85%] flex-col gap-3">
							<div
								class={msg.role === 'user'
									? 'bg-primary rounded-2xl rounded-br-sm px-4 py-2.5 text-white'
									: 'rounded-2xl rounded-tl-sm bg-white px-4 py-2.5 shadow-sm dark:bg-gray-900'}
							>
								<!-- eslint-disable-next-line svelte/no-at-html-tags -->
								{@html md(msg.text)}
							</div>

							{#if msg.choices && !msg.answered}
								<div class="flex flex-wrap gap-2">
									{#each msg.choices as c (c.value)}
										<button
											class="border-primary/40 hover:bg-primary/10 rounded-full border px-3 py-1.5 text-sm transition {selected.includes(
												c.value
											)
												? 'bg-primary/15 border-primary'
												: ''}"
											onclick={() => {
												if (step === 99) return policies(msg, c.value);
												if (msg.multi) {
													selected = selected.includes(c.value)
														? selected.filter((x) => x !== c.value)
														: [...selected, c.value];
												} else answer(msg, [c.value]);
											}}
										>
											{#if msg.multi && selected.includes(c.value)}<Check
													class="mr-1 inline size-3.5"
												/>{/if}{c.label}
										</button>
									{/each}
									{#if msg.multi}
										<button
											disabled={selected.length === 0}
											class="bg-primary rounded-full px-4 py-1.5 text-sm text-white disabled:opacity-40"
											onclick={() => answer(msg, selected)}
										>
											Continue
										</button>
									{/if}
								</div>
							{/if}

							{#if msg.kind === 'sources'}
								<div class="flex flex-col gap-2">
									{#each sources as s, idx (s.key)}
										{@const Icon = s.icon}
										<div
											class="flex items-center gap-3 rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-gray-900"
										>
											<span class="w-4 text-xs text-gray-400">{idx + 1}</span>
											<Icon class="size-5 shrink-0 text-gray-500" />
											<div class="min-w-0 grow">
												<div class="font-medium">{s.name}</div>
												<div class="text-xs text-gray-500">{s.why}</div>
												{#if s.found}
													<div in:fade class="mt-0.5 text-xs text-green-600 dark:text-green-400">
														{s.found}
													</div>
												{/if}
											</div>
											{#if s.status === 'connected'}
												<span
													class="flex items-center gap-1 text-sm text-green-600 dark:text-green-400"
													><Check class="size-4" /> Connected</span
												>
											{:else if s.status === 'connecting'}
												<Loader2 class="size-4 animate-spin text-gray-400" />
											{:else}
												<button
													class="border-primary text-primary hover:bg-primary/10 rounded-lg border px-3 py-1 text-sm"
													onclick={() => connect(s)}>Connect</button
												>
											{/if}
										</div>
									{/each}
									<div class="flex gap-2">
										{#if !allConnected}
											<button
												class="border-primary text-primary rounded-lg border px-3 py-1.5 text-sm"
												onclick={connectAll}>Connect all</button
											>
										{/if}
										{#if !collectStarted}
											<button
												class="bg-primary flex items-center gap-1 rounded-lg px-3 py-1.5 text-sm text-white disabled:opacity-40"
												disabled={sources.filter((s) => s.status === 'connected').length < 2}
												onclick={startCollect}
												>Run first collection <ArrowRight class="size-4" /></button
											>
										{/if}
									</div>
								</div>
							{/if}

							{#if msg.kind === 'collect'}
								<div class="flex flex-col gap-1 text-sm text-gray-600 dark:text-gray-400">
									{#each sources.filter((s) => s.status === 'connected') as s (s.key)}
										<div class="flex items-center gap-2">
											{#if collecting}<Loader2 class="size-3.5 animate-spin" />{:else}<Check
													class="size-3.5 text-green-600"
												/>{/if}
											{s.name}
										</div>
									{/each}
								</div>
							{/if}

							{#if msg.kind === 'policies'}
								<div class="flex flex-col gap-2">
									{#each risks as r, idx (idx)}
										<div
											in:fly={{ y: 6, delay: idx * 120 }}
											class="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-gray-900"
										>
											<div class="flex items-center gap-2">
												<ShieldAlert class="text-primary size-4" />
												<span class="font-medium">{r.policy}</span>
											</div>
											<div class="mt-1 flex items-center gap-2 text-xs text-gray-500">
												<span class="rounded" style="padding:1px 6px;{tag.pol}">Monitor</span>
												Treats: {r.title}
											</div>
										</div>
									{/each}
									<button class="bg-primary self-start rounded-lg px-3 py-1.5 text-sm text-white"
										>Review and enable</button
									>
								</div>
							{/if}
						</div>
					</div>
				{/each}
				{#if thinking}
					<div class="flex gap-3" in:fade>
						<div
							class="bg-primary/10 text-primary flex size-8 shrink-0 items-center justify-center rounded-full"
						>
							<Sparkles class="size-4" />
						</div>
						<div
							class="flex items-center gap-1 rounded-2xl bg-white px-4 py-3 shadow-sm dark:bg-gray-900"
						>
							<span class="size-2 animate-bounce rounded-full bg-gray-400"></span>
							<span class="size-2 animate-bounce rounded-full bg-gray-400 [animation-delay:150ms]"
							></span>
							<span class="size-2 animate-bounce rounded-full bg-gray-400 [animation-delay:300ms]"
							></span>
						</div>
					</div>
				{/if}
			</div>
		</div>

		<!-- Register panel -->
		<div
			style="width:440px;flex-shrink:0"
			class="flex flex-col overflow-y-auto bg-white p-5 dark:bg-gray-950"
		>
			<h2 class="mb-4 text-sm font-semibold tracking-wide text-gray-500 uppercase">
				Your risk profile
			</h2>

			<div class="mb-5 flex flex-col gap-3 text-sm">
				<div>
					<div class="mb-1 text-xs text-gray-500">Frameworks in scope</div>
					{#if frameworks.length}
						<div class="flex flex-wrap gap-1">
							{#each frameworks as f (f)}
								<span in:fade class="rounded-md" style="padding:1px 6px;{tag.fw}">{f}</span>
							{/each}
						</div>
					{:else}<div class="text-gray-400">…</div>{/if}
				</div>
				<div>
					<div class="mb-1 text-xs text-gray-500">Usage segments</div>
					{#if segments.length}
						<div class="flex flex-wrap gap-1">
							{#each segments as s (s)}
								<span in:fade class="rounded-md" style="padding:1px 6px;{tag.seg}">{s}</span>
							{/each}
						</div>
					{:else}<div class="text-gray-400">…</div>{/if}
				</div>
				<div>
					<div class="mb-1 text-xs text-gray-500">Data sources</div>
					{#if sources.length}
						<div class="flex flex-col gap-0.5">
							{#each sources as s (s.key)}
								<div class="flex items-center gap-1.5">
									{#if s.status === 'connected'}<Check
											class="size-3.5 text-green-600"
										/>{:else if s.status === 'connecting'}<Loader2
											class="size-3.5 animate-spin"
										/>{:else}<span class="size-3.5 rounded-full border border-gray-300"></span>{/if}
									{s.name}
								</div>
							{/each}
						</div>
					{:else}<div class="text-gray-400">…</div>{/if}
				</div>
			</div>

			<div class="mb-2 flex items-center justify-between" style="margin-top:20px">
				<h2 class="text-sm font-semibold tracking-wide text-gray-500 uppercase">Risk register</h2>
				{#if risks.length}<span class="text-xs text-gray-500">{risks.length} open</span>{/if}
				{#if collecting}<Loader2 class="size-4 animate-spin text-gray-400" />{/if}
			</div>
			{#if risks.length === 0}
				<div
					class="rounded-xl border border-dashed border-gray-300 p-6 text-center text-sm text-gray-400 dark:border-gray-700"
				>
					Risks will appear here after the first collection.
				</div>
			{/if}
			<div class="flex flex-col gap-2">
				{#each risks as r, idx (idx)}
					<div
						in:fly={{ x: 20, duration: 300 }}
						class="rounded-xl border border-gray-200 p-3 dark:border-gray-800"
					>
						<div class="flex items-start gap-2">
							<span
								class="rounded font-bold uppercase"
								style="margin-top:2px;padding:2px 6px;font-size:10px;white-space:nowrap;{sevStyle[
									r.severity
								]}">{r.severity}</span
							>
							<div class="font-medium leading-snug">{r.title}</div>
						</div>
						<div class="mt-1.5 text-xs text-gray-500">{r.evidence}</div>
						<div class="mt-1.5 flex flex-wrap gap-1 text-[11px]">
							<span class="rounded" style="padding:1px 6px;{tag.fw}">{r.framework}</span>
							<span class="rounded" style="padding:1px 6px;{tag.seg}">{r.segment}</span>
							{#if policiesShown}
								<span in:fade class="rounded" style="padding:1px 6px;{tag.pol}">Policy drafted</span
								>
							{/if}
						</div>
					</div>
				{/each}
			</div>
		</div>
	</div>
</div>
