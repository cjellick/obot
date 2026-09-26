<script lang="ts">
	import { page } from '$app/state';
	import Layout from '$lib/components/Layout.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import SearchUsers from '$lib/components/admin/SearchUsers.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import type { OrgGroup, OrgUser } from '$lib/services';
	import {
		createVirtualKey,
		formatCost,
		formatNumber,
		getVirtualKey,
		listBudgetBindings,
		listProviders,
		setBudgetBindings,
		updateVirtualKey,
		type Budget,
		type Provider,
		type VirtualKey,
		type VirtualKeyInput,
		type VirtualKeyProviderConfig
	} from '$lib/services/llm-gateway/gateway';
	import { providerLabel } from '$lib/services/llm-gateway/providers';
	import { profile } from '$lib/stores';
	import { success } from '$lib/stores/success';
	import { convertSubjectsToTableData, resolveSubjects } from '$lib/subjectResolver';
	import { goto } from '$lib/url';
	import { Plus, Trash2 } from '@lucide/svelte';
	import { onMount } from 'svelte';

	const durations = [
		{ id: '1m', label: 'Minute' },
		{ id: '1h', label: 'Hour' },
		{ id: '1d', label: 'Day' },
		{ id: '1w', label: 'Week' },
		{ id: '1M', label: 'Month' },
		{ id: '1Q', label: 'Quarter' },
		{ id: '1Y', label: 'Year' }
	];

	type Subject = { type: 'user' | 'group' | 'selector'; id: string };

	let id = $derived(page.params.id ?? 'new');
	let isNew = $derived(id === 'new');
	let existing = $state<VirtualKey>();
	let providers = $state<Provider[]>([]);
	let loading = $state(true);
	let saving = $state(false);
	let error = $state('');
	let searchDialog = $state<ReturnType<typeof SearchUsers>>();

	let name = $state('');
	let description = $state('');
	let active = $state(true);
	let allProviders = $state(true);
	let providerConfigs = $state<(VirtualKeyProviderConfig & { modelsText: string })[]>([]);
	let budgets = $state<Budget[]>([]);
	let requestLimit = $state<number | undefined>();
	let requestWindow = $state('1h');
	let tokenLimit = $state<number | undefined>();
	let tokenWindow = $state('1d');
	let subjects = $state<Subject[]>([]);
	let resolved = $state<{ users: OrgUser[]; groups: OrgGroup[] }>({ users: [], groups: [] });

	let readonly = $derived(profile.current.isAdminReadonly?.());

	onMount(async () => {
		try {
			providers = await listProviders();
			if (!isNew) {
				existing = await getVirtualKey(id);
				name = existing.name;
				description = existing.description ?? '';
				active = existing.is_active !== false;
				allProviders = !!existing.allow_all_providers;
				providerConfigs = (existing.provider_configs ?? []).map((pc) => ({
					...pc,
					modelsText: (pc.allowed_models ?? ['*']).join(', ')
				}));
				budgets = (existing.budgets ?? []).map((b) => ({ ...b }));
				const r = existing.rate_limit;
				requestLimit = r?.request_max_limit ?? undefined;
				requestWindow = r?.request_reset_duration ?? '1h';
				tokenLimit = r?.token_max_limit ?? undefined;
				tokenWindow = r?.token_reset_duration ?? '1d';
				const bindings = (await listBudgetBindings()).filter((b) => b.virtualKeyID === id);
				subjects = bindings.map((b) => ({
					type: b.subjectType === 'everyone' ? 'selector' : b.subjectType,
					id: b.subjectType === 'everyone' ? '*' : (b.subjectID ?? '')
				}));
				resolved = await resolveSubjects(subjects);
			}
		} finally {
			loading = false;
		}
	});

	function toInput(): VirtualKeyInput {
		const rateLimit =
			requestLimit || tokenLimit
				? {
						request_max_limit: requestLimit || null,
						request_reset_duration: requestLimit ? requestWindow : null,
						token_max_limit: tokenLimit || null,
						token_reset_duration: tokenLimit ? tokenWindow : null
					}
				: undefined;
		return {
			name: name.trim(),
			description,
			is_active: active,
			allow_all_providers: allProviders,
			provider_configs: allProviders
				? []
				: providerConfigs.map((pc) => ({
						...(pc.id ? { id: pc.id } : {}),
						provider: pc.provider,
						weight: pc.weight ?? 1,
						allowed_models: pc.modelsText
							.split(',')
							.map((s) => s.trim())
							.filter(Boolean)
					})),
			budgets: budgets
				.filter((b) => b.max_limit > 0)
				.map((b) => ({ max_limit: Number(b.max_limit), reset_duration: b.reset_duration })),
			rate_limit: rateLimit
		};
	}

	async function save() {
		saving = true;
		error = '';
		try {
			const input = toInput();
			const vk = isNew
				? await createVirtualKey(input, { dontLogErrors: true })
				: await updateVirtualKey(id, input, { dontLogErrors: true });
			await setBudgetBindings(
				vk.id,
				subjects.map((s) =>
					s.type === 'selector'
						? { subjectType: 'everyone' }
						: { subjectType: s.type, subjectID: s.id }
				)
			);
			success.add(isNew ? 'Budget created.' : 'Budget saved.');
			goto('/llm-gateway/virtual-keys');
		} catch (e) {
			const msg = e instanceof Error ? e.message : String(e);
			error = /"message":"([^"]+)"/.exec(msg)?.[1] ?? msg;
		} finally {
			saving = false;
		}
	}

	let valid = $derived(
		name.trim().length > 0 &&
			(allProviders || providerConfigs.length > 0) &&
			providerConfigs.every((pc) => !!pc.provider)
	);

	let subjectRows = $derived(convertSubjectsToTableData(subjects, resolved.users, resolved.groups));
</script>

<svelte:head>
	<title>Obot | {isNew ? 'New Budget' : name}</title>
</svelte:head>

<Layout
	title={isNew ? 'New Budget' : name || 'Budget'}
	showBackButton
	onBackButtonClick={() => goto('/llm-gateway/virtual-keys')}
	classes={{ childrenContainer: 'max-w-5xl' }}
>
	{#if loading}
		<div class="flex justify-center p-12"><Loading class="size-8" /></div>
	{:else}
		<div class="flex flex-col gap-8 pb-24">
			<section class="paper flex flex-col gap-4 p-4">
				<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
					<div class="flex flex-col gap-1">
						<label for="vk-name" class="text-sm font-light">Name</label>
						<input id="vk-name" class="text-input-filled" bind:value={name} disabled={readonly} />
					</div>
					<div class="flex items-center pt-6">
						<Toggle
							label="Active"
							labelInline
							checked={active}
							disabled={readonly}
							onChange={(v) => (active = v)}
						/>
					</div>
					<div class="flex flex-col gap-1 md:col-span-2">
						<label for="vk-description" class="text-sm font-light">Description</label>
						<input
							id="vk-description"
							class="text-input-filled"
							bind:value={description}
							disabled={readonly}
						/>
					</div>
				</div>
			</section>

			<section class="flex flex-col gap-2">
				<div class="flex items-center justify-between">
					<div>
						<h2 class="text-lg font-semibold">Assigned to</h2>
						<p class="text-muted-content text-xs">
							Requests from these users and groups are counted against this budget.
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
					noDataMessage="Not assigned. The budget only applies once it is assigned."
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

			<section class="flex flex-col gap-2">
				<div class="flex items-center justify-between">
					<div>
						<h2 class="text-lg font-semibold">Spend budgets</h2>
						<p class="text-muted-content text-xs">
							Requests are rejected once spend reaches the limit, until the window resets.
						</p>
					</div>
					{#if !readonly}
						<button
							class="btn btn-secondary flex items-center gap-1 text-sm"
							onclick={() => budgets.push({ max_limit: 100, reset_duration: '1M' })}
						>
							<Plus class="size-4" /> Add budget
						</button>
					{/if}
				</div>
				<div class="paper flex flex-col gap-3 p-4">
					{#each budgets as b, i (i)}
						<div class="flex flex-wrap items-end gap-3">
							<div class="flex flex-col gap-1">
								<label for={`budget-${i}`} class="text-sm font-light">Limit (USD)</label>
								<input
									id={`budget-${i}`}
									type="number"
									min="0"
									step="0.01"
									class="text-input-filled w-40"
									bind:value={b.max_limit}
									disabled={readonly}
								/>
							</div>
							<div class="flex flex-col gap-1">
								<label for={`budget-window-${i}`} class="text-sm font-light">Resets every</label>
								<select
									id={`budget-window-${i}`}
									class="select"
									bind:value={b.reset_duration}
									disabled={readonly}
								>
									{#each durations as d (d.id)}<option value={d.id}>{d.label}</option>{/each}
								</select>
							</div>
							{#if b.current_usage !== undefined}
								<div class="flex flex-col gap-1">
									<span class="text-sm font-light">Used</span>
									<div class="flex items-center gap-2">
										<progress
											class="progress progress-primary w-40"
											value={Math.min(b.current_usage, b.max_limit)}
											max={b.max_limit || 1}
										></progress>
										<span class="text-xs"
											>{formatCost(b.current_usage)} of {formatCost(b.max_limit)}</span
										>
									</div>
								</div>
							{/if}
							{#if !readonly}
								<IconButton
									variant="danger"
									tooltip={{ text: 'Remove budget' }}
									onclick={() => budgets.splice(i, 1)}
								>
									<Trash2 class="size-4" />
								</IconButton>
							{/if}
						</div>
					{:else}
						<span class="text-muted-content text-sm">No spend budget.</span>
					{/each}
				</div>
			</section>

			<section class="flex flex-col gap-2">
				<h2 class="text-lg font-semibold">Rate limits</h2>
				<div class="paper grid grid-cols-1 gap-4 p-4 md:grid-cols-2">
					<div class="flex items-end gap-3">
						<div class="flex flex-1 flex-col gap-1">
							<label for="rl-requests" class="text-sm font-light">Max requests</label>
							<input
								id="rl-requests"
								type="number"
								min="0"
								class="text-input-filled"
								placeholder="Unlimited"
								bind:value={requestLimit}
								disabled={readonly}
							/>
						</div>
						<select
							class="select"
							bind:value={requestWindow}
							disabled={readonly}
							aria-label="Request window"
						>
							{#each durations as d (d.id)}<option value={d.id}>per {d.label}</option>{/each}
						</select>
					</div>
					<div class="flex items-end gap-3">
						<div class="flex flex-1 flex-col gap-1">
							<label for="rl-tokens" class="text-sm font-light">Max tokens</label>
							<input
								id="rl-tokens"
								type="number"
								min="0"
								class="text-input-filled"
								placeholder="Unlimited"
								bind:value={tokenLimit}
								disabled={readonly}
							/>
						</div>
						<select
							class="select"
							bind:value={tokenWindow}
							disabled={readonly}
							aria-label="Token window"
						>
							{#each durations as d (d.id)}<option value={d.id}>per {d.label}</option>{/each}
						</select>
					</div>
					{#if existing?.rate_limit}
						<p class="text-muted-content text-xs md:col-span-2">
							Current window: {formatNumber(existing.rate_limit.request_current_usage)} requests,
							{formatNumber(existing.rate_limit.token_current_usage)} tokens.
						</p>
					{/if}
				</div>
			</section>

			<section class="flex flex-col gap-2">
				<h2 class="text-lg font-semibold">Providers &amp; models</h2>
				<div class="paper flex flex-col gap-4 p-4">
					<Toggle
						label="Allow every provider and model"
						labelInline
						checked={allProviders}
						disabled={readonly}
						onChange={(v) => (allProviders = v)}
					/>
					{#if !allProviders}
						{#each providerConfigs as pc, i (i)}
							<div class="flex flex-wrap items-end gap-3">
								<div class="flex flex-col gap-1">
									<label for={`pc-provider-${i}`} class="text-sm font-light">Provider</label>
									<select
										id={`pc-provider-${i}`}
										class="select"
										bind:value={pc.provider}
										disabled={readonly}
									>
										{#each providers as p (p.name)}
											<option value={p.name}>{providerLabel(p.name)}</option>
										{/each}
									</select>
								</div>
								<div class="flex min-w-64 flex-1 flex-col gap-1">
									<label for={`pc-models-${i}`} class="text-sm font-light">Allowed models</label>
									<input
										id={`pc-models-${i}`}
										class="text-input-filled"
										bind:value={pc.modelsText}
										placeholder="* or gpt-4.1, gpt-4o-mini"
										disabled={readonly}
									/>
								</div>
								<div class="flex flex-col gap-1">
									<label for={`pc-weight-${i}`} class="text-sm font-light">Weight</label>
									<input
										id={`pc-weight-${i}`}
										type="number"
										min="0"
										step="0.1"
										class="text-input-filled w-24"
										bind:value={pc.weight}
										disabled={readonly}
									/>
								</div>
								{#if !readonly}
									<IconButton
										variant="danger"
										tooltip={{ text: 'Remove' }}
										onclick={() => providerConfigs.splice(i, 1)}
									>
										<Trash2 class="size-4" />
									</IconButton>
								{/if}
							</div>
						{/each}
						{#if !readonly}
							<button
								class="btn btn-secondary flex w-fit items-center gap-1 text-sm"
								onclick={() =>
									providerConfigs.push({
										provider: providers[0]?.name ?? '',
										weight: 1,
										modelsText: '*'
									})}
							>
								<Plus class="size-4" /> Add provider
							</button>
						{/if}
						<p class="text-muted-content text-xs">
							Requests for other providers or models are rejected. When several providers serve a
							request without an explicit provider, traffic is split by weight.
						</p>
					{/if}
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
				<button class="btn btn-secondary text-sm" onclick={() => goto('/llm-gateway/virtual-keys')}>
					Cancel
				</button>
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
				.map((g) => ({
					type: g.id === '*' ? ('selector' as const) : ('group' as const),
					id: g.id
				}))
		];
		resolved = {
			users: [...resolved.users, ...users],
			groups: [...resolved.groups, ...groups]
		};
	}}
/>
