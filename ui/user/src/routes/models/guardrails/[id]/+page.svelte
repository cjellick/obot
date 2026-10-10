<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import Layout from '$lib/components/Layout.svelte';
	import Select from '$lib/components/Select.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import SearchUsers from '$lib/components/admin/SearchUsers.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import { parseErrorContent } from '$lib/errors';
	import Loading from '$lib/icons/Loading.svelte';
	import type { OrgGroup, OrgUser } from '$lib/services';
	import {
		ACTION_LABELS,
		APPLY_TO_LABELS,
		createGuardrailRule,
		getGuardrailRule,
		listGuardrailProfiles,
		listGuardrailProviders,
		updateGuardrailRule,
		validateGuardrailCondition,
		type GuardrailAction,
		type GuardrailApplyTo,
		type GuardrailInputScope,
		type GuardrailProfile,
		type GuardrailProvider,
		type GuardrailRule
	} from '$lib/services/llm-gateway/guardrails';
	import { profile } from '$lib/stores';
	import { success } from '$lib/stores/success';
	import { convertSubjectsToTableData, resolveSubjects } from '$lib/subjectResolver';
	import { goto } from '$lib/url';
	import { Plus, Trash2, TriangleAlert } from '@lucide/svelte';
	import { onMount } from 'svelte';

	type Subject = { type: 'user' | 'group' | 'selector'; id: string };

	// Rules on this page guard model traffic. MCP guardrails are configured as MCP server filters.
	const TARGET = 'llm';
	const RULES_URL = '/models?view=guardrails';

	const conditionVariables = [
		{ name: 'model', description: 'Requested model, as "provider/model"' },
		{ name: 'provider', description: 'Provider name' },
		{ name: 'params', description: 'Request parameters, e.g. params.temperature' },
		{ name: 'headers', description: 'Request headers (map of strings)' },
		{ name: 'user', description: 'Caller, with user.id and user.groups' }
	];
	const conditionExample = 'model.startsWith("openai/") && !("admins" in user.groups)';

	const applyToOptions = Object.entries(APPLY_TO_LABELS).map(([id, label]) => ({ id, label }));
	const actionOptions = Object.entries(ACTION_LABELS).map(([id, label]) => ({ id, label }));
	const inputScopeOptions = [
		{ id: 'latest', label: 'Only new messages since the last response' },
		{ id: 'all', label: 'Every message in the request' }
	];
	const selectClass =
		'bg-base-200 dark:bg-base-200 dark:border-base-400 border border-transparent shadow-inner';

	let id = $derived(page.params.id ?? 'new');
	let isNew = $derived(id === 'new');
	let loading = $state(true);
	let saving = $state(false);
	let error = $state('');
	let searchDialog = $state<ReturnType<typeof SearchUsers>>();

	let name = $state('');
	let description = $state('');
	let enabled = $state(true);
	let applyTo = $state<GuardrailApplyTo>('both');
	let action = $state<GuardrailAction>('block');
	let condition = $state('');
	let samplingRate = $state(100);
	let inputScope = $state<GuardrailInputScope>('latest');
	let failClosed = $state(false);
	let blockMessage = $state('');
	let profileIDs = $state<string[]>([]);
	let subjects = $state<Subject[]>([]);
	let resolved = $state<{ users: OrgUser[]; groups: OrgGroup[] }>({ users: [], groups: [] });

	let detectors = $state<GuardrailProfile[]>([]);
	let providers = $state<GuardrailProvider[]>([]);
	let conditionError = $state('');
	let validating = $state(false);

	let readonly = $derived(profile.current.isAdminReadonly?.());

	onMount(async () => {
		try {
			[detectors, providers] = await Promise.all([
				listGuardrailProfiles(),
				listGuardrailProviders()
			]);
			if (isNew) return;
			const rule = await getGuardrailRule(id);
			name = rule.name;
			description = rule.description ?? '';
			enabled = rule.enabled;
			applyTo = rule.applyTo;
			action = rule.action;
			condition = rule.condition ?? '';
			samplingRate = rule.samplingRate || 100;
			inputScope = rule.inputScope || 'latest';
			failClosed = rule.failClosed;
			blockMessage = rule.blockMessage ?? '';
			profileIDs = rule.profileIDs ?? [];
			subjects = (rule.subjects ?? []).map((s) => ({
				type: s.type === 'everyone' ? 'selector' : s.type,
				id: s.type === 'everyone' ? '*' : (s.id ?? '')
			}));
			resolved = await resolveSubjects(subjects);
		} finally {
			loading = false;
		}
	});

	// Validate the condition as it's typed, ignoring answers superseded by later edits.
	let validationSeq = 0;
	$effect(() => {
		const expr = condition.trim();
		const seq = ++validationSeq;
		if (!expr) {
			conditionError = '';
			validating = false;
			return;
		}
		validating = true;
		const timer = setTimeout(async () => {
			try {
				const result = await validateGuardrailCondition(TARGET, expr, { dontLogErrors: true });
				if (seq === validationSeq) conditionError = result.valid ? '' : (result.error ?? '');
			} catch (e) {
				if (seq === validationSeq) conditionError = parseErrorContent(e).message;
			} finally {
				if (seq === validationSeq) validating = false;
			}
		}, 400);
		return () => clearTimeout(timer);
	});

	let providersByType = $derived(Object.fromEntries(providers.map((p) => [p.type, p])));
	let nonRedacting = $derived(
		action === 'redact'
			? detectors.filter(
					(p) => p.id && profileIDs.includes(p.id) && providersByType[p.type]?.redacts === false
				)
			: []
	);

	let valid = $derived(
		name.trim().length > 0 &&
			profileIDs.length > 0 &&
			samplingRate >= 1 &&
			samplingRate <= 100 &&
			!conditionError &&
			!validating
	);

	async function save() {
		saving = true;
		error = '';
		try {
			const body: GuardrailRule = {
				name: name.trim(),
				description,
				enabled,
				target: TARGET,
				applyTo,
				action,
				condition: condition.trim(),
				samplingRate: Number(samplingRate),
				inputScope,
				failClosed,
				blockMessage: blockMessage.trim(),
				profileIDs,
				subjects: subjects.map((s) =>
					s.type === 'selector' ? { type: 'everyone' } : { type: s.type, id: s.id }
				)
			};
			if (isNew) {
				await createGuardrailRule(body, { dontLogErrors: true });
			} else {
				await updateGuardrailRule(id, body, { dontLogErrors: true });
			}
			success.add(isNew ? 'Guardrail rule created.' : 'Guardrail rule saved.');
			goto(RULES_URL);
		} catch (e) {
			error = parseErrorContent(e).message;
		} finally {
			saving = false;
		}
	}

	let subjectRows = $derived(convertSubjectsToTableData(subjects, resolved.users, resolved.groups));
</script>

<svelte:head>
	<title>Obot | {isNew ? 'New Model Guardrail Rule' : name}</title>
</svelte:head>

<Layout
	title={isNew ? 'New Model Guardrail Rule' : name || 'Model Guardrail Rule'}
	showBackButton
	onBackButtonClick={() => goto(RULES_URL)}
>
	{#if loading && !isNew}
		<Skeleton type="items" count={4} class="h-32" />
	{:else}
		<div class="flex flex-col gap-6">
			<section class="paper">
				<div>
					<h2 class="text-lg font-semibold">Details</h2>
					<p class="text-muted-content text-sm">
						What the rule checks in model requests and responses, and what happens when a detector
						finds something.
					</p>
				</div>
				<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
					<div class="flex flex-col gap-1">
						<label for="rule-name" class="text-sm font-light">Name</label>
						<input id="rule-name" class="text-input-filled" bind:value={name} disabled={readonly} />
					</div>
					<div class="flex items-center pt-6">
						<Toggle
							label="Enabled"
							labelInline
							checked={enabled}
							disabled={readonly}
							onChange={(v) => (enabled = v)}
						/>
					</div>
					<div class="flex flex-col gap-1 md:col-span-2">
						<label for="rule-description" class="text-sm font-light">Description</label>
						<input
							id="rule-description"
							class="text-input-filled"
							bind:value={description}
							disabled={readonly}
						/>
					</div>
					<div class="flex flex-col gap-1">
						<span id="rule-apply-to-label" class="text-sm font-light">Apply to</span>
						<Select
							id="rule-apply-to"
							class={selectClass}
							ariaLabelledby="rule-apply-to-label"
							options={applyToOptions}
							selected={applyTo}
							onSelect={(option) => (applyTo = option.id as GuardrailApplyTo)}
							disabled={readonly}
						/>
						<span class="text-muted-content text-xs">
							Input is the prompt sent to the model; output is its response, including streams.
						</span>
					</div>
					<div class="flex flex-col gap-1">
						<span id="rule-action-label" class="text-sm font-light">Action</span>
						<Select
							id="rule-action"
							class={selectClass}
							ariaLabelledby="rule-action-label"
							options={actionOptions}
							selected={action}
							onSelect={(option) => (action = option.id as GuardrailAction)}
							disabled={readonly}
						/>
						<span class="text-muted-content text-xs">
							{action === 'block'
								? 'Rejects the request or response when any detector finds a problem.'
								: action === 'redact'
									? 'Masks what the detectors find and lets the rest through.'
									: 'Lets the traffic through and records an event.'}
						</span>
					</div>
					<div class="flex flex-col gap-1">
						<label for="rule-sampling" class="text-sm font-light">Sampling rate (%)</label>
						<input
							id="rule-sampling"
							type="number"
							min="1"
							max="100"
							class="text-input-filled"
							bind:value={samplingRate}
							disabled={readonly}
						/>
						<span class="text-muted-content text-xs">
							Percentage of matching traffic the rule checks.
						</span>
					</div>
					{#if applyTo !== 'output'}
						<div class="flex flex-col gap-1">
							<span id="rule-input-scope-label" class="text-sm font-light">Input scope</span>
							<Select
								id="rule-input-scope"
								class={selectClass}
								ariaLabelledby="rule-input-scope-label"
								options={inputScopeOptions}
								selected={inputScope}
								onSelect={(option) => (inputScope = option.id as GuardrailInputScope)}
								disabled={readonly}
							/>
							<span class="text-muted-content text-xs">
								Redacting rules always check every message.
							</span>
						</div>
					{/if}
				</div>
				<Toggle
					label="Fail closed: block when a detector can't be evaluated"
					labelInline
					checked={failClosed}
					disabled={readonly}
					onChange={(v) => (failClosed = v)}
				/>
				{#if action !== 'log'}
					<div class="flex flex-col gap-1">
						<label for="rule-block-message" class="text-sm font-light">Block message</label>
						<input
							id="rule-block-message"
							class="text-input-filled"
							placeholder="Leave empty for the default message"
							bind:value={blockMessage}
							disabled={readonly}
						/>
					</div>
				{/if}
			</section>

			<section class="paper">
				<div class="flex flex-wrap items-center justify-between gap-2">
					<div>
						<h2 class="text-lg font-semibold">Detectors</h2>
						<p class="text-muted-content text-sm">
							The checks this rule runs. Any finding from any selected detector triggers the action.
						</p>
					</div>
					{#if !readonly}
						<a href={resolve('/guardrails?view=detectors')} class="text-link text-sm">
							Create a detector
						</a>
					{/if}
				</div>
				<div class="grid grid-cols-1 gap-1 md:grid-cols-2">
					{#each detectors as p (p.id)}
						<label class="flex items-center gap-2 text-sm">
							<input
								type="checkbox"
								class="checkbox checkbox-sm"
								checked={!!p.id && profileIDs.includes(p.id)}
								disabled={readonly}
								onchange={(e) => {
									const checked = (e.target as HTMLInputElement).checked;
									profileIDs = checked
										? [...profileIDs, p.id!]
										: profileIDs.filter((x) => x !== p.id);
								}}
							/>
							<span>{p.name}</span>
							<span class="text-muted-content text-xs">
								{providersByType[p.type]?.label ?? p.type}
							</span>
						</label>
					{:else}
						<span class="text-muted-content text-sm">No detectors yet.</span>
					{/each}
				</div>
				{#if nonRedacting.length}
					<div class="notification-alert flex items-start gap-2 rounded-md p-3 text-sm">
						<TriangleAlert class="size-4 shrink-0" />
						<span>
							{nonRedacting.map((p) => p.name).join(', ')}
							{nonRedacting.length === 1 ? 'uses a provider that' : 'use providers that'} can't locate
							findings for redaction. Findings from {nonRedacting.length === 1 ? 'it' : 'them'} will block
							instead.
						</span>
					</div>
				{/if}
			</section>

			<section class="paper">
				<div class="flex flex-wrap items-center justify-between gap-2">
					<div>
						<h2 class="text-lg font-semibold">Assigned to</h2>
						<p class="text-muted-content text-sm">
							The rule applies to traffic from these users and groups.
						</p>
					</div>
					{#if !readonly}
						<button
							class="btn btn-primary flex items-center gap-1 text-sm"
							onclick={() => searchDialog?.open()}
						>
							<Plus class="size-4" /> Add User/Group
						</button>
					{/if}
				</div>
				<Table
					data={subjectRows}
					fields={['displayName', 'type']}
					headers={[{ property: 'displayName', title: 'Name' }]}
					noDataMessage="Not limited to anyone. The rule applies to everyone."
				>
					{#snippet actions(row)}
						{#if !readonly}
							<IconButton
								variant="danger"
								tooltip={{ text: 'Remove' }}
								onclick={() => (subjects = subjects.filter((s) => s.id !== row.id))}
							>
								<Trash2 class="size-4" />
							</IconButton>
						{/if}
					{/snippet}
				</Table>
			</section>

			<section class="paper">
				<div>
					<h2 class="text-lg font-semibold">Condition</h2>
					<p class="text-muted-content text-sm">
						An optional <a
							href="https://cel.dev"
							target="_blank"
							rel="noopener noreferrer"
							class="text-link">CEL</a
						> expression. The rule only applies when it is true. Leave empty to apply to all matching
						traffic.
					</p>
				</div>
				<div class="flex flex-col gap-2">
					<textarea
						id="rule-condition"
						aria-label="Condition"
						class="text-input-filled min-h-20 font-mono text-sm"
						placeholder={conditionExample}
						bind:value={condition}
						disabled={readonly}></textarea>
					{#if conditionError}
						<span class="text-error text-xs" role="alert">{conditionError}</span>
					{/if}
					<div class="text-muted-content grid grid-cols-1 gap-x-4 gap-y-1 text-xs md:grid-cols-2">
						{#each conditionVariables as v (v.name)}
							<span><code class="text-base-content">{v.name}</code> — {v.description}</span>
						{/each}
					</div>
				</div>
			</section>

			{#if error}
				<div class="notification-error rounded-md p-3 text-sm">{error}</div>
			{/if}
		</div>

		{#if !readonly}
			<div
				class="bg-base-200 dark:bg-base-100 sticky bottom-0 left-0 z-40 flex w-full justify-end gap-2 py-4"
			>
				<button class="btn btn-secondary text-sm" onclick={() => goto(RULES_URL)}>Cancel</button>
				<button class="btn btn-primary text-sm" disabled={!valid || saving} onclick={save}>
					{#if saving}<Loading class="size-4" />{:else}Save{/if}
				</button>
			</div>
		{/if}
	{/if}
</Layout>

<SearchUsers
	bind:this={searchDialog}
	filterIds={subjects.map((s) => s.id)}
	onAdd={async (users: OrgUser[], groups: OrgGroup[]) => {
		const have = new Set(subjects.map((s) => s.id));
		subjects = [
			...subjects,
			...users.filter((u) => !have.has(u.id)).map((u) => ({ type: 'user' as const, id: u.id })),
			...groups
				.filter((g) => !have.has(g.id))
				.map((g) => ({ type: g.id === '*' ? ('selector' as const) : ('group' as const), id: g.id }))
		];
		resolved = { users: [...resolved.users, ...users], groups: [...resolved.groups, ...groups] };
	}}
/>
