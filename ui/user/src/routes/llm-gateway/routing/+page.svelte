<script lang="ts">
	import Confirm from '$lib/components/Confirm.svelte';
	import Layout from '$lib/components/Layout.svelte';
	import ResponsiveDialog from '$lib/components/ResponsiveDialog.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import GatewayStatusBanner from '$lib/components/llm-gateway/GatewayStatusBanner.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		createRoutingRule,
		deleteRoutingRule,
		getStatus,
		listProviders,
		listRoutingRules,
		parseFallback,
		updateRoutingRule,
		type GatewayStatus,
		type Provider,
		type RoutingRule
	} from '$lib/services/llm-gateway/gateway';
	import { providerLabel } from '$lib/services/llm-gateway/providers';
	import { profile } from '$lib/stores';
	import { success } from '$lib/stores/success';
	import { GitFork, Pencil, Plus, Trash2 } from '@lucide/svelte';
	import { onMount } from 'svelte';

	type TargetRow = { provider: string; model: string; weight: number };
	type FallbackRow = { provider: string; model: string };

	const examples = [
		{ label: 'Model is requested', cel: 'model == "gpt-4o"' },
		{ label: 'Model family', cel: 'model.startsWith("claude-")' },
		{ label: 'Provider is requested', cel: 'provider == "openai"' },
		{ label: 'Header value', cel: 'headers["x-tier"] == "premium"' },
		{ label: 'Always', cel: 'true' }
	];

	let status = $state<GatewayStatus>();
	let rules = $state<RoutingRule[]>([]);
	let providers = $state<Provider[]>([]);
	let loading = $state(true);
	let dialog = $state<ReturnType<typeof ResponsiveDialog>>();
	let toDelete = $state<RoutingRule>();

	let editing = $state<RoutingRule>();
	let name = $state('');
	let description = $state('');
	let enabled = $state(true);
	let cel = $state('');
	let priority = $state(0);
	let targets = $state<TargetRow[]>([]);
	let fallbacks = $state<FallbackRow[]>([]);
	let saving = $state(false);
	let error = $state('');

	let readonly = $derived(profile.current.isAdminReadonly?.());

	async function load() {
		loading = true;
		try {
			status = await getStatus();
			if (!status.ready) return;
			[rules, providers] = await Promise.all([listRoutingRules(), listProviders()]);
			rules.sort((a, b) => (a.priority ?? 0) - (b.priority ?? 0));
		} finally {
			loading = false;
		}
	}

	onMount(load);

	function open(rule?: RoutingRule) {
		editing = rule;
		error = '';
		name = rule?.name ?? '';
		description = rule?.description ?? '';
		enabled = rule?.enabled ?? true;
		cel = rule?.cel_expression ?? 'model == "gpt-4o"';
		priority = rule?.priority ?? 0;
		targets = (rule?.targets ?? [{ provider: providers[0]?.name ?? '', model: '', weight: 1 }]).map(
			(t) => ({ provider: t.provider ?? '', model: t.model ?? '', weight: t.weight })
		);
		fallbacks = (rule?.fallbacks ?? []).map(parseFallback);
		dialog?.open();
	}

	let weightSum = $derived(targets.reduce((s, t) => s + Number(t.weight || 0), 0));
	let valid = $derived(
		name.trim().length > 0 &&
			cel.trim().length > 0 &&
			targets.length > 0 &&
			Math.abs(weightSum - 1) < 0.0001
	);

	async function save() {
		saving = true;
		error = '';
		try {
			const body: RoutingRule = {
				name: name.trim(),
				description,
				enabled,
				cel_expression: cel.trim(),
				priority: Number(priority),
				scope: 'global',
				targets: targets.map((t) => ({
					provider: t.provider || null,
					model: t.model.trim() || null,
					weight: Number(t.weight)
				})),
				fallbacks: fallbacks.filter((f) => f.provider && f.model.trim())
			};
			if (editing?.id) {
				await updateRoutingRule(editing.id, body, { dontLogErrors: true });
			} else {
				await createRoutingRule(body, { dontLogErrors: true });
			}
			dialog?.close();
			success.add('Routing rule saved.');
			await load();
		} catch (e) {
			const msg = e instanceof Error ? e.message : String(e);
			error = /"message":"([^"]+)"/.exec(msg)?.[1] ?? msg;
		} finally {
			saving = false;
		}
	}

	function targetLabel(r: RoutingRule) {
		return r.targets
			.map(
				(t) =>
					`${t.provider ? providerLabel(t.provider) : 'same provider'} / ${t.model || 'same model'} (${Math.round(t.weight * 100)}%)`
			)
			.join(', ');
	}

	let rows = $derived(
		rules.map((r) => ({
			...r,
			id: r.id ?? r.name,
			targetsLabel: targetLabel(r),
			fallbacksLabel:
				(r.fallbacks ?? [])
					.map((f) => {
						const p = parseFallback(f);
						return `${p.provider}/${p.model}`;
					})
					.join(' → ') || '—',
			enabledLabel: r.enabled === false ? 'Disabled' : 'Enabled'
		}))
	);
</script>

<svelte:head>
	<title>Obot | LLM Routing</title>
</svelte:head>

<Layout title="Routing" classes={{ childrenContainer: 'max-w-none' }}>
	{#snippet rightNavActions()}
		{#if !readonly && status?.ready}
			<button class="btn btn-primary flex items-center gap-1 text-sm" onclick={() => open()}>
				<Plus class="size-4" /> Add Rule
			</button>
		{/if}
	{/snippet}

	<div class="flex flex-col gap-6">
		<p class="text-muted-content max-w-4xl text-sm">
			Routing rules redirect matching requests before they reach a provider: send a model to a
			cheaper or faster one, split traffic across providers by weight for A/B tests or migrations,
			and fail over through a chain of fallback models. Rules are evaluated in priority order and
			match with a <a
				class="text-link"
				href="https://github.com/google/cel-spec"
				target="_blank"
				rel="noreferrer">CEL</a
			>
			expression over <code>model</code>, <code>provider</code> and <code>headers</code>. Obot
			checks access to the model a user asks for; the rule decides where it is served.
		</p>

		<GatewayStatusBanner {status} showNoProviders={false} />

		{#if loading}
			<div class="flex justify-center p-12"><Loading class="size-8" /></div>
		{:else if status?.ready && rules.length === 0}
			<div class="mt-12 flex w-md flex-col items-center gap-4 self-center text-center">
				<GitFork class="text-base-content/80 size-24 opacity-25" />
				<h4 class="text-muted-content text-lg font-semibold">No routing rules</h4>
				<p class="text-muted-content text-sm font-light">
					Requests go to the provider and model they name. Add a rule to redirect, split or fail
					over traffic.
				</p>
			</div>
		{:else}
			<Table
				data={rows}
				fields={[
					'priority',
					'name',
					'cel_expression',
					'targetsLabel',
					'fallbacksLabel',
					'enabledLabel'
				]}
				headers={[
					{ property: 'priority', title: 'Priority' },
					{ property: 'name', title: 'Name' },
					{ property: 'cel_expression', title: 'When' },
					{ property: 'targetsLabel', title: 'Route to' },
					{ property: 'fallbacksLabel', title: 'Fallbacks' },
					{ property: 'enabledLabel', title: 'Status' }
				]}
				onClickRow={readonly
					? undefined
					: (row) => open(rules.find((r) => (r.id ?? r.name) === row.id))}
			>
				{#snippet onRenderColumn(property, row)}
					{#if property === 'cel_expression'}
						<code class="text-xs">{row.cel_expression}</code>
					{:else}
						{row[property as keyof typeof row]}
					{/if}
				{/snippet}
				{#snippet actions(row)}
					{#if !readonly}
						<IconButton
							tooltip={{ text: 'Edit rule' }}
							onclick={(e) => {
								e.stopPropagation();
								open(rules.find((r) => (r.id ?? r.name) === row.id));
							}}
						>
							<Pencil class="size-4" />
						</IconButton>
						<IconButton
							variant="danger"
							tooltip={{ text: 'Delete rule' }}
							onclick={(e) => {
								e.stopPropagation();
								toDelete = row;
							}}
						>
							<Trash2 class="size-4" />
						</IconButton>
					{/if}
				{/snippet}
			</Table>
		{/if}
	</div>
</Layout>

<ResponsiveDialog
	bind:this={dialog}
	title={editing ? 'Edit Routing Rule' : 'Add Routing Rule'}
	class="max-w-3xl"
>
	<form
		class="flex flex-col gap-4 pt-4"
		onsubmit={(e) => {
			e.preventDefault();
			save();
		}}
	>
		<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
			<div class="flex flex-col gap-1 md:col-span-2">
				<label for="rule-name" class="text-sm font-light">Name</label>
				<input id="rule-name" class="text-input-filled" bind:value={name} />
			</div>
			<div class="flex flex-col gap-1">
				<label for="rule-priority" class="text-sm font-light">Priority</label>
				<input id="rule-priority" type="number" class="text-input-filled" bind:value={priority} />
			</div>
			<div class="flex flex-col gap-1 md:col-span-3">
				<label for="rule-description" class="text-sm font-light">Description</label>
				<input id="rule-description" class="text-input-filled" bind:value={description} />
			</div>
			<div class="flex flex-col gap-1 md:col-span-3">
				<label for="rule-cel" class="text-sm font-light">When (CEL expression)</label>
				<input id="rule-cel" class="text-input-filled font-mono text-sm" bind:value={cel} />
				<div class="flex flex-wrap gap-1">
					{#each examples as ex (ex.cel)}
						<button
							type="button"
							class="bg-base-200 dark:bg-base-300 rounded px-2 py-0.5 text-xs"
							onclick={() => (cel = ex.cel)}>{ex.label}</button
						>
					{/each}
				</div>
			</div>
		</div>

		<div class="flex flex-col gap-2">
			<div class="flex items-center justify-between">
				<span class="text-sm font-medium">Route to</span>
				<span
					class={`text-xs ${Math.abs(weightSum - 1) < 0.0001 ? 'text-muted-content' : 'text-error'}`}
				>
					Weights must add up to 1 (currently {weightSum.toFixed(2)})
				</span>
			</div>
			{#each targets as t, i (i)}
				<div class="flex flex-wrap items-center gap-2">
					<select class="select" bind:value={t.provider} aria-label="Target provider">
						<option value="">Same provider</option>
						{#each providers as p (p.name)}<option value={p.name}>{providerLabel(p.name)}</option
							>{/each}
					</select>
					<input
						class="text-input-filled min-w-48 flex-1"
						placeholder="Model (empty keeps the requested model)"
						bind:value={t.model}
						aria-label="Target model"
					/>
					<input
						type="number"
						min="0"
						max="1"
						step="0.05"
						class="text-input-filled w-24"
						bind:value={t.weight}
						aria-label="Target weight"
					/>
					<IconButton
						variant="danger"
						tooltip={{ text: 'Remove' }}
						onclick={() => targets.splice(i, 1)}
					>
						<Trash2 class="size-4" />
					</IconButton>
				</div>
			{/each}
			<button
				type="button"
				class="btn btn-secondary flex w-fit items-center gap-1 text-xs"
				onclick={() => targets.push({ provider: providers[0]?.name ?? '', model: '', weight: 0 })}
			>
				<Plus class="size-3" /> Add target
			</button>
		</div>

		<div class="flex flex-col gap-2">
			<span class="text-sm font-medium">Fallbacks</span>
			<span class="text-muted-content text-xs"
				>Tried in order when the target fails after its retries.</span
			>
			{#each fallbacks as f, i (i)}
				<div class="flex flex-wrap items-center gap-2">
					<select class="select" bind:value={f.provider} aria-label="Fallback provider">
						{#each providers as p (p.name)}<option value={p.name}>{providerLabel(p.name)}</option
							>{/each}
					</select>
					<input
						class="text-input-filled min-w-48 flex-1"
						placeholder="Model"
						bind:value={f.model}
						aria-label="Fallback model"
					/>
					<IconButton
						variant="danger"
						tooltip={{ text: 'Remove' }}
						onclick={() => fallbacks.splice(i, 1)}
					>
						<Trash2 class="size-4" />
					</IconButton>
				</div>
			{/each}
			<button
				type="button"
				class="btn btn-secondary flex w-fit items-center gap-1 text-xs"
				onclick={() => fallbacks.push({ provider: providers[0]?.name ?? '', model: '' })}
			>
				<Plus class="size-3" /> Add fallback
			</button>
		</div>

		<Toggle label="Enabled" labelInline checked={enabled} onChange={(v) => (enabled = v)} />

		{#if error}
			<div class="notification-error rounded-md p-2 text-sm">{error}</div>
		{/if}

		<div class="flex justify-end gap-2">
			<button type="button" class="btn btn-secondary text-sm" onclick={() => dialog?.close()}>
				Cancel
			</button>
			<button type="submit" class="btn btn-primary text-sm" disabled={!valid || saving}>
				{#if saving}<Loading class="size-4" />{:else}Save{/if}
			</button>
		</div>
	</form>
</ResponsiveDialog>

<Confirm
	show={!!toDelete}
	msg={`Delete routing rule ${toDelete?.name}?`}
	onsuccess={async () => {
		if (toDelete?.id) await deleteRoutingRule(toDelete.id);
		toDelete = undefined;
		await load();
	}}
	oncancel={() => (toDelete = undefined)}
/>
