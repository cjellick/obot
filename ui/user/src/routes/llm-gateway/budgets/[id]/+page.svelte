<script lang="ts">
	import { page } from '$app/state';
	import Layout from '$lib/components/Layout.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import SearchUsers from '$lib/components/admin/SearchUsers.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import { CommonModelProviderIds } from '$lib/constants';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		AdminService,
		UserService,
		type Model,
		type OrgGroup,
		type OrgUser
	} from '$lib/services';
	import {
		createBudget,
		formatMetric,
		getBudget,
		getBudgetUsage,
		METRIC_LABELS,
		updateBudget,
		windowLabel,
		type Budget,
		type BudgetLimit,
		type BudgetMetric,
		type BudgetUsageEntry
	} from '$lib/services/llm-gateway/gateway';
	import { profile } from '$lib/stores';
	import { success } from '$lib/stores/success';
	import { convertSubjectsToTableData, resolveSubjects } from '$lib/subjectResolver';
	import { goto } from '$lib/url';
	import { Plus, Trash2 } from '@lucide/svelte';
	import { onMount } from 'svelte';

	const units = [
		{ id: 'h', label: 'hour' },
		{ id: 'd', label: 'day' },
		{ id: 'w', label: 'week' },
		{ id: 'M', label: 'month' }
	];
	const metrics = Object.entries(METRIC_LABELS) as [BudgetMetric, string][];

	type Subject = { type: 'user' | 'group' | 'selector'; id: string };
	type LimitRow = {
		metric: BudgetMetric;
		amount: number;
		count: number;
		unit: string;
		calendar: boolean;
	};

	let id = $derived(page.params.id ?? 'new');
	let isNew = $derived(id === 'new');
	let loading = $state(true);
	let saving = $state(false);
	let error = $state('');
	let searchDialog = $state<ReturnType<typeof SearchUsers>>();

	let name = $state('');
	let description = $state('');
	let enabled = $state(true);
	let perUser = $state(true);
	let exemptAdmins = $state(false);
	let userTokenLimits = $state(false);
	let limits = $state<LimitRow[]>([]);
	let selectedModels = $state<string[]>([]);
	let modelSearch = $state('');
	let subjects = $state<Subject[]>([]);
	let resolved = $state<{ users: OrgUser[]; groups: OrgGroup[] }>({ users: [], groups: [] });
	let gatewayModels = $state<Model[]>([]);
	let usage = $state<BudgetUsageEntry[]>([]);
	let savedLimits = $state<BudgetLimit[]>([]);
	let userNames = $state<Record<string, string>>({});

	let readonly = $derived(profile.current.isAdminReadonly?.());

	function toRow(l: BudgetLimit): LimitRow {
		const m = /^(\d+)([mhdwM])$/.exec(l.window);
		return {
			metric: l.metric,
			amount: l.amount,
			count: m ? Number(m[1]) : 1,
			unit: m ? m[2] : 'd',
			calendar: !!l.calendar
		};
	}

	function toLimit(r: LimitRow): BudgetLimit {
		return {
			metric: r.metric,
			amount: Number(r.amount),
			window: `${r.calendar ? 1 : Math.max(1, Number(r.count))}${r.unit}`,
			calendar: r.calendar
		};
	}

	onMount(async () => {
		try {
			const models = await AdminService.listModels({ all: true }).catch(() => [] as Model[]);
			gatewayModels = models.filter((m) => m.modelProvider === CommonModelProviderIds.LLM_GATEWAY);
			if (isNew) {
				limits = [{ metric: 'spend', amount: 50, count: 1, unit: 'M', calendar: true }];
				return;
			}
			const budget = await getBudget(id);
			name = budget.name;
			description = budget.description ?? '';
			enabled = budget.enabled;
			perUser = budget.perUser;
			exemptAdmins = budget.exemptAdmins;
			userTokenLimits = budget.userTokenLimits ?? false;
			limits = budget.limits.map(toRow);
			savedLimits = budget.limits;
			selectedModels = budget.models ?? [];
			subjects = budget.subjects.map((s) => ({
				type: s.type === 'everyone' ? 'selector' : s.type,
				id: s.type === 'everyone' ? '*' : (s.id ?? '')
			}));
			resolved = await resolveSubjects(subjects);
			usage = await getBudgetUsage(id);
			if (budget.perUser && usage.length) {
				const users = await UserService.listUsers().catch(() => []);
				userNames = Object.fromEntries(
					users.map((u) => [u.id, u.displayName || u.email || u.username || u.id])
				);
			}
		} finally {
			loading = false;
		}
	});

	let valid = $derived(
		name.trim().length > 0 && limits.length > 0 && limits.every((l) => Number(l.amount) >= 0)
	);

	async function save() {
		saving = true;
		error = '';
		try {
			const body: Budget = {
				name: name.trim(),
				description,
				enabled,
				perUser,
				exemptAdmins,
				userTokenLimits: perUser && userTokenLimits,
				limits: limits.map(toLimit),
				models: selectedModels,
				subjects: subjects.map((s) =>
					s.type === 'selector' ? { type: 'everyone' } : { type: s.type, id: s.id }
				)
			};
			if (isNew) {
				await createBudget(body, { dontLogErrors: true });
			} else {
				await updateBudget(id, body, { dontLogErrors: true });
			}
			success.add(isNew ? 'Budget created.' : 'Budget saved.');
			goto('/llm-gateway/budgets');
		} catch (e) {
			const msg = e instanceof Error ? e.message : String(e);
			error = /"?error"?:\s*"([^"]+)"/.exec(msg)?.[1] ?? msg;
		} finally {
			saving = false;
		}
	}

	let subjectRows = $derived(convertSubjectsToTableData(subjects, resolved.users, resolved.groups));
	let visibleModels = $derived(
		gatewayModels.filter(
			(m) =>
				!modelSearch || (m.targetModel ?? m.name).toLowerCase().includes(modelSearch.toLowerCase())
		)
	);
	let usageRows = $derived(
		[...usage]
			.sort((a, b) => (b.used[0] ?? 0) - (a.used[0] ?? 0))
			.map((u) => ({
				...u,
				id: u.userID ?? 'shared',
				label: u.userID ? (userNames[u.userID] ?? u.userID) : 'Everyone (shared)'
			}))
	);
</script>

<svelte:head>
	<title>Obot | {isNew ? 'New Budget' : name}</title>
</svelte:head>

<Layout
	title={isNew ? 'New Budget' : name || 'Budget'}
	showBackButton
	onBackButtonClick={() => goto('/llm-gateway/budgets')}
	classes={{ childrenContainer: 'max-w-5xl' }}
>
	{#if loading && !isNew}
		<div class="flex justify-center p-12"><Loading class="size-8" /></div>
	{:else}
		<div class="flex flex-col gap-8 pb-24">
			<section class="paper flex flex-col gap-4 p-4">
				<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
					<div class="flex flex-col gap-1">
						<label for="budget-name" class="text-sm font-light">Name</label>
						<input
							id="budget-name"
							class="text-input-filled"
							bind:value={name}
							disabled={readonly}
						/>
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
						<label for="budget-description" class="text-sm font-light">Description</label>
						<input
							id="budget-description"
							class="text-input-filled"
							bind:value={description}
							disabled={readonly}
						/>
					</div>
				</div>
				<div class="flex flex-col gap-2">
					<span class="text-sm font-light">Allowance</span>
					<label class="flex items-center gap-2 text-sm">
						<input
							type="radio"
							class="radio radio-sm"
							name="scope"
							checked={perUser}
							onchange={() => (perUser = true)}
							disabled={readonly}
						/>
						Each assigned user gets these limits individually
					</label>
					<label class="flex items-center gap-2 text-sm">
						<input
							type="radio"
							class="radio radio-sm"
							name="scope"
							checked={!perUser}
							onchange={() => (perUser = false)}
							disabled={readonly}
						/>
						One pool shared by everyone assigned
					</label>
				</div>
				<Toggle
					label="Don't apply to administrators"
					labelInline
					checked={exemptAdmins}
					disabled={readonly}
					onChange={(v) => (exemptAdmins = v)}
				/>
				{#if perUser}
					<Toggle
						label="Users' personal daily token limits replace this budget's token limits"
						labelInline
						checked={userTokenLimits}
						disabled={readonly}
						onChange={(v) => (userTokenLimits = v)}
					/>
				{/if}
			</section>

			<section class="flex flex-col gap-2">
				<div class="flex items-center justify-between">
					<div>
						<h2 class="text-lg font-semibold">Limits</h2>
						<p class="text-muted-content text-xs">
							Requests are rejected once any limit is reached. Rolling windows free up as usage ages
							out; calendar windows reset at the start of the hour, day, week or month (UTC).
						</p>
					</div>
					{#if !readonly}
						<button
							class="btn btn-secondary flex items-center gap-1 text-sm"
							onclick={() =>
								limits.push({
									metric: 'tokens',
									amount: 1000000,
									count: 1,
									unit: 'd',
									calendar: false
								})}
						>
							<Plus class="size-4" /> Add limit
						</button>
					{/if}
				</div>
				<div class="paper flex flex-col gap-3 p-4">
					{#each limits as l, i (i)}
						<div class="flex flex-wrap items-end gap-3">
							<div class="flex flex-col gap-1">
								<label for={`limit-metric-${i}`} class="text-sm font-light">Metric</label>
								<select
									id={`limit-metric-${i}`}
									class="select"
									bind:value={l.metric}
									disabled={readonly}
								>
									{#each metrics as [value, label] (value)}<option {value}>{label}</option>{/each}
								</select>
							</div>
							<div class="flex flex-col gap-1">
								<label for={`limit-amount-${i}`} class="text-sm font-light">Limit</label>
								<input
									id={`limit-amount-${i}`}
									type="number"
									min="0"
									step={l.metric === 'spend' ? '0.01' : '1'}
									class="text-input-filled w-40"
									bind:value={l.amount}
									disabled={readonly}
								/>
							</div>
							<div class="flex flex-col gap-1">
								<span class="text-sm font-light">Window</span>
								<div class="flex items-center gap-2">
									<select
										class="select w-32"
										aria-label="Window type"
										bind:value={l.calendar}
										disabled={readonly}
									>
										<option value={false}>Rolling</option>
										<option value={true}>Calendar</option>
									</select>
									{#if !l.calendar}
										<input
											type="number"
											min="1"
											class="text-input-filled w-20"
											aria-label="Window length"
											bind:value={l.count}
											disabled={readonly}
										/>
									{/if}
									<select
										class="select w-28"
										aria-label="Window unit"
										bind:value={l.unit}
										disabled={readonly}
									>
										{#each units as u (u.id)}<option value={u.id}
												>{u.label}{!l.calendar && l.count > 1 ? 's' : ''}</option
											>{/each}
									</select>
								</div>
							</div>
							{#if !readonly}
								<IconButton
									variant="danger"
									tooltip={{ text: 'Remove limit' }}
									onclick={() => limits.splice(i, 1)}
								>
									<Trash2 class="size-4" />
								</IconButton>
							{/if}
						</div>
					{:else}
						<span class="text-muted-content text-sm">Add at least one limit.</span>
					{/each}
				</div>
			</section>

			<section class="flex flex-col gap-2">
				<div class="flex items-center justify-between">
					<div>
						<h2 class="text-lg font-semibold">Assigned to</h2>
						<p class="text-muted-content text-xs">
							The budget applies to requests from these users and groups.
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
				<div>
					<h2 class="text-lg font-semibold">Models</h2>
					<p class="text-muted-content text-xs">
						Only requests for the selected models count against this budget. Select none to cover
						every model.
					</p>
				</div>
				<div class="paper flex flex-col gap-2 p-4">
					<input class="text-input-filled" placeholder="Search models" bind:value={modelSearch} />
					<div class="grid max-h-64 grid-cols-1 gap-1 overflow-y-auto md:grid-cols-2">
						{#each visibleModels as m (m.id)}
							<label class="flex items-center gap-2 text-sm">
								<input
									type="checkbox"
									class="checkbox checkbox-sm"
									checked={selectedModels.includes(m.id)}
									disabled={readonly}
									onchange={(e) => {
										const checked = (e.target as HTMLInputElement).checked;
										selectedModels = checked
											? [...selectedModels, m.id]
											: selectedModels.filter((x) => x !== m.id);
									}}
								/>
								<span class="font-mono text-xs">{m.targetModel ?? m.name}</span>
							</label>
						{:else}
							<span class="text-muted-content text-sm">No gateway models.</span>
						{/each}
					</div>
					<span class="text-muted-content text-xs">
						{selectedModels.length ? `${selectedModels.length} selected` : 'All models'}
					</span>
				</div>
			</section>

			{#if !isNew && savedLimits.length}
				<section class="flex flex-col gap-2">
					<h2 class="text-lg font-semibold">Current usage</h2>
					<div class="paper overflow-x-auto p-0">
						<table class="table-sm table w-full text-sm">
							<thead>
								<tr class="text-muted-content text-xs">
									<th class="text-left">{perUser ? 'User' : ''}</th>
									{#each savedLimits as l, i (i)}
										<th class="text-left">{METRIC_LABELS[l.metric]} {windowLabel(l)}</th>
									{/each}
								</tr>
							</thead>
							<tbody>
								{#each usageRows as row (row.id)}
									<tr>
										<td>{row.label}</td>
										{#each savedLimits as l, i (i)}
											{@const used = row.used[i] ?? 0}
											<td>
												<div class="flex items-center gap-2">
													<progress
														class={`progress w-24 ${used >= l.amount ? 'progress-error' : 'progress-primary'}`}
														value={Math.min(used, l.amount)}
														max={l.amount || 1}
													></progress>
													<span class="text-xs"
														>{formatMetric(l.metric, used)} / {formatMetric(
															l.metric,
															l.amount
														)}</span
													>
												</div>
											</td>
										{/each}
									</tr>
								{:else}
									<tr
										><td colspan={savedLimits.length + 1} class="text-muted-content p-4 text-center"
											>No usage in the current window.</td
										></tr
									>
								{/each}
							</tbody>
						</table>
					</div>
				</section>
			{/if}

			{#if error}
				<div class="notification-error rounded-md p-3 text-sm">{error}</div>
			{/if}
		</div>

		{#if !readonly}
			<div
				class="bg-base-200 dark:bg-base-100 sticky bottom-0 left-0 z-40 flex w-full justify-end gap-2 py-4"
			>
				<button class="btn btn-secondary text-sm" onclick={() => goto('/llm-gateway/budgets')}
					>Cancel</button
				>
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
