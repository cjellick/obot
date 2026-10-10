<script lang="ts">
	import { resolve } from '$app/paths';
	import { MCP_FILTERS_FIELD_IDS, PAGE_TRANSITION_DURATION } from '$lib/constants';
	import { parseErrorContent } from '$lib/errors';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		AdminService,
		type MCPFilter,
		type MCPFilterInput,
		type MCPFilterManifest,
		type MCPFilterResource,
		type MCPFilterWebhookSelector,
		type MCPGuardrailDetector,
		type MCPGuardrailDetectorProvider,
		type MCPGuardrailFilterAction,
		type MCPGuardrailFilterDirection,
		type OrgGroup,
		type OrgUser
	} from '$lib/services';
	import { convertSubjectsToTableData, resolveSubjects } from '$lib/subjectResolver';
	import Toggle from '../Toggle.svelte';
	import IconButton from '../primitives/IconButton.svelte';
	import Table from '../table/Table.svelte';
	import SearchUsers from './SearchUsers.svelte';
	import SelectorsAndResourcesFormSegment from './SelectorsAndResourcesFormSegment.svelte';
	import { Plus, Trash2, TriangleAlert } from '@lucide/svelte';
	import { onMount, untrack, type Snippet } from 'svelte';
	import { fly } from 'svelte/transition';
	import { twMerge } from 'tailwind-merge';

	interface Props {
		topContent?: Snippet;
		filter?: MCPFilterInput;
		onCreate?: (filter?: MCPFilter) => void;
		onUpdate?: (filter?: MCPFilter) => void;
		readonly?: boolean;
	}

	let { topContent, filter: initialFilter, onCreate, onUpdate, readonly }: Props = $props();

	const duration = PAGE_TRANSITION_DURATION;
	const CONDITION_EXAMPLE = 'mcp_server == "github" && mcp_tool.startsWith("create_")';

	const actionOptions: { value: MCPGuardrailFilterAction; label: string; description: string }[] = [
		{
			value: 'block',
			label: 'Block',
			description: 'Rejects the tool call or result when any detector finds a problem.'
		},
		{
			value: 'redact',
			label: 'Redact',
			description: 'Masks what the detectors find and lets the rest through.'
		},
		{
			value: 'log',
			label: 'Log only',
			description: 'Lets the traffic through and records an event.'
		}
	];
	const directionOptions: { value: MCPGuardrailFilterDirection; label: string }[] = [
		{ value: 'request', label: 'Requests (tool call arguments)' },
		{ value: 'response', label: 'Responses (tool results)' },
		{ value: 'both', label: 'Requests and responses' }
	];
	const conditionVariables = [
		{ name: 'mcp_server', description: 'MCP server name' },
		{ name: 'mcp_tool', description: 'Tool name' },
		{ name: 'mcp_arguments', description: 'Tool call arguments (map)' },
		{ name: 'headers', description: 'Request headers (map of strings)' },
		{ name: 'user', description: 'Caller, with user.id and user.groups' }
	];

	let initialFilterId = $derived(initialFilter?.id);

	let filter = $state<{
		name: string;
		resources: MCPFilterResource[];
		selectors: MCPFilterWebhookSelector[];
		disabled: boolean;
		detectorIDs: string[];
		action: MCPGuardrailFilterAction;
		direction: MCPGuardrailFilterDirection;
		failClosed: boolean;
		samplingRate: number;
		condition: string;
		blockMessage: string;
	}>(
		untrack(() => {
			const guardrail = initialFilter?.guardrail;
			return {
				name: initialFilter?.name ?? '',
				resources: initialFilter?.resources ?? [{ id: 'default', type: 'mcpCatalog' }],
				selectors: initialFilter?.selectors ?? [],
				disabled: initialFilter?.disabled ?? false,
				detectorIDs: guardrail?.detectorIDs ?? [],
				action: guardrail?.action ?? 'block',
				direction: guardrail?.direction ?? 'both',
				failClosed: guardrail?.failClosed ?? false,
				samplingRate: guardrail?.samplingRate || 100,
				condition: guardrail?.condition ?? '',
				blockMessage: guardrail?.blockMessage ?? ''
			};
		})
	);

	// The "everyone" subject is shown as the "All Obot Users" selector, as in access policies.
	type Subject = { type: 'user' | 'group' | 'selector'; id: string };
	let subjects = $state<Subject[]>(
		untrack(() =>
			(initialFilter?.guardrail?.subjects ?? []).map((s) =>
				s.type === 'everyone'
					? { type: 'selector' as const, id: '*' }
					: { type: s.type, id: s.id ?? '' }
			)
		)
	);
	let resolved = $state<{ users: OrgUser[]; groups: OrgGroup[] }>({ users: [], groups: [] });
	let searchUsersDialog = $state<ReturnType<typeof SearchUsers>>();

	let detectors = $state<MCPGuardrailDetector[]>([]);
	let providers = $state<MCPGuardrailDetectorProvider[]>([]);
	let detectorsLoading = $state(true);
	let detectorsLoadError = $state('');

	let saving = $state(false);
	let saveError = $state('');
	let showValidation = $state(false);
	let conditionError = $state('');
	let validatingCondition = $state(false);

	onMount(async () => {
		try {
			[detectors, providers] = await Promise.all([
				AdminService.listMCPGuardrailDetectors(),
				AdminService.listMCPGuardrailDetectorProviders().catch(
					() => [] as MCPGuardrailDetectorProvider[]
				)
			]);
		} catch (err) {
			detectorsLoadError = parseErrorContent(err).message;
		} finally {
			detectorsLoading = false;
		}

		if (subjects.length > 0) {
			resolved = await resolveSubjects(subjects).catch(() => resolved);
		}
	});

	// Validate the condition as it's typed, ignoring answers superseded by later edits.
	let validationSeq = 0;
	$effect(() => {
		const expr = filter.condition.trim();
		const seq = ++validationSeq;
		if (!expr) {
			conditionError = '';
			validatingCondition = false;
			return;
		}
		validatingCondition = true;
		const timer = setTimeout(async () => {
			try {
				const result = await AdminService.validateMCPGuardrailCondition(expr, {
					dontLogErrors: true
				});
				if (seq === validationSeq) conditionError = result.valid ? '' : (result.error ?? '');
			} catch (err) {
				if (seq === validationSeq) conditionError = parseErrorContent(err).message;
			} finally {
				if (seq === validationSeq) validatingCondition = false;
			}
		}, 400);
		return () => clearTimeout(timer);
	});

	let providersByType = $derived(new Map(providers.map((p) => [p.type, p])));
	let detectorsById = $derived(new Map(detectors.map((d) => [d.id, d])));
	// Selected detectors that no longer exist (deleted since the filter was saved).
	let missingDetectorIDs = $derived(filter.detectorIDs.filter((id) => !detectorsById.has(id)));
	let nonRedacting = $derived(
		filter.action === 'redact'
			? detectors.filter(
					(d) => filter.detectorIDs.includes(d.id) && providersByType.get(d.type)?.redacts === false
				)
			: []
	);
	let subjectRows = $derived(convertSubjectsToTableData(subjects, resolved.users, resolved.groups));

	let nameError = $derived(showValidation && !filter.name.trim());
	let detectorsError = $derived(showValidation && filter.detectorIDs.length === 0);
	let samplingRateError = $derived(
		showValidation &&
			(!Number.isFinite(Number(filter.samplingRate)) ||
				Number(filter.samplingRate) < 1 ||
				Number(filter.samplingRate) > 100)
	);

	function toggleDetector(id: string, checked: boolean) {
		filter.detectorIDs = checked
			? [...filter.detectorIDs, id]
			: filter.detectorIDs.filter((d) => d !== id);
	}

	function buildManifest(): MCPFilterManifest {
		const selectors =
			filter.selectors.length > 0
				? filter.selectors
						.map((s) => ({
							...s,
							identifiers: s.identifiers?.filter((id) => id.trim()) || []
						}))
						.filter((s) => s.method || (s.identifiers && s.identifiers.length > 0))
				: undefined;
		const condition = filter.condition.trim();
		const blockMessage = filter.action === 'log' ? '' : filter.blockMessage.trim();

		return {
			name: filter.name.trim(),
			resources: filter.resources,
			selectors,
			disabled: filter.disabled,
			guardrail: {
				detectorIDs: filter.detectorIDs,
				action: filter.action,
				direction: filter.direction,
				failClosed: filter.failClosed,
				samplingRate: Number(filter.samplingRate),
				subjects: subjects.map((s) =>
					s.type === 'selector' ? { type: 'everyone' as const } : { type: s.type, id: s.id }
				),
				...(condition ? { condition } : {}),
				...(blockMessage ? { blockMessage } : {})
			}
		};
	}

	async function handleSave() {
		const samplingRate = Number(filter.samplingRate);
		if (
			!filter.name.trim() ||
			filter.detectorIDs.length === 0 ||
			!Number.isFinite(samplingRate) ||
			samplingRate < 1 ||
			samplingRate > 100
		) {
			showValidation = true;
			return;
		}
		if (conditionError || validatingCondition) {
			return;
		}

		saving = true;
		saveError = '';
		try {
			const manifest = buildManifest();
			if (initialFilterId) {
				const result = await AdminService.updateMCPFilter(initialFilterId, manifest);
				onUpdate?.(result);
			} else {
				const result = await AdminService.createMCPFilter(manifest);
				onCreate?.(result);
			}
		} catch (err) {
			saveError = parseErrorContent(err).message;
		} finally {
			saving = false;
		}
	}
</script>

<div
	class="flex h-full w-full flex-col gap-4"
	out:fly={{ x: 100, duration }}
	in:fly={{ x: 100, delay: duration }}
>
	<div class="flex grow flex-col gap-4" out:fly={{ x: -100, duration }} in:fly={{ x: -100 }}>
		{#if topContent}
			{@render topContent()}
		{/if}
		{#if !initialFilterId}
			<h1 class="text-2xl font-semibold">Create Guardrail Filter</h1>
		{/if}

		<div
			class="dark:bg-base-200 dark:border-base-400 bg-base-100 rounded-lg border border-transparent p-4"
			id={MCP_FILTERS_FIELD_IDS.basicDetails}
		>
			<div class="flex flex-col gap-6">
				<div class="flex flex-col gap-2">
					<label for="filter-name" class="flex-1 text-sm font-light capitalize"> Name </label>
					<div class="flex grow flex-col gap-0.5">
						<input
							id="filter-name"
							bind:value={filter.name}
							class="text-input-filled dark:bg-base-100 mt-0.5 {nameError
								? 'border-error focus:border-error focus:ring-error'
								: ''}"
							disabled={readonly}
						/>
						{#if nameError}
							<p class="text-xs text-error">Name is required</p>
						{/if}
					</div>
				</div>
				<div class="flex flex-col gap-1">
					<span class="text-sm font-light">Type</span>
					<p class="text-sm">Guardrail</p>
					<p class="text-muted-content text-xs">
						Runs guardrail detectors on MCP traffic inside Obot. No server is deployed for this
						filter.
					</p>
				</div>
			</div>
		</div>

		<div
			class="dark:bg-base-200 dark:border-base-400 bg-base-100 flex flex-col gap-6 rounded-lg border border-transparent p-4 shadow-sm"
			id={MCP_FILTERS_FIELD_IDS.runtimeFormDetails}
		>
			<div class="flex flex-col gap-2">
				<div class="flex items-start justify-between gap-4">
					<div class="flex flex-col gap-1">
						<span id="guardrail-detectors-label" class="text-sm font-light">Detectors</span>
						<p class="text-muted-content text-xs">
							Any finding from any selected detector triggers the action.
						</p>
					</div>
					<a href={resolve('/guardrails?view=detectors')} class="text-link shrink-0 text-xs"
						>Manage detectors</a
					>
				</div>
				<div
					role="group"
					aria-labelledby="guardrail-detectors-label"
					class={twMerge(
						'bg-base-200 dark:bg-base-100 rounded-lg p-3 shadow-inner',
						detectorsError && 'border-error border'
					)}
				>
					{#if detectorsLoading}
						<Loading class="size-4" />
					{:else if detectorsLoadError}
						<p class="text-xs text-error">Failed to load detectors: {detectorsLoadError}</p>
					{:else}
						<div class="grid grid-cols-1 gap-2 md:grid-cols-2">
							{#each detectors as detector (detector.id)}
								<label class="flex items-start gap-2 text-sm">
									<input
										type="checkbox"
										class="checkbox checkbox-sm mt-0.5"
										checked={filter.detectorIDs.includes(detector.id)}
										disabled={readonly}
										onchange={(e) =>
											toggleDetector(detector.id, (e.target as HTMLInputElement).checked)}
									/>
									<span class="flex flex-col">
										<span>
											{detector.name}
											<span class="text-muted-content text-xs">
												{providersByType.get(detector.type)?.label ?? detector.type}
											</span>
										</span>
										{#if detector.error}
											<span class="text-xs text-error">{detector.error}</span>
										{/if}
									</span>
								</label>
							{:else}
								<p class="text-muted-content text-sm">
									No detectors yet. Create one from "Manage detectors".
								</p>
							{/each}
						</div>
					{/if}
					{#if missingDetectorIDs.length > 0 && !detectorsLoading && !detectorsLoadError}
						<p class="mt-2 text-xs text-error">
							{missingDetectorIDs.length === 1 ? 'A selected detector' : 'Some selected detectors'} no
							longer exist: {missingDetectorIDs.join(', ')}
						</p>
					{/if}
				</div>
				{#if detectorsError}
					<p class="text-xs text-error">Select at least one detector</p>
				{/if}
			</div>

			<div class="grid grid-cols-1 gap-6 md:grid-cols-2">
				<div class="flex flex-col gap-2">
					<label for="guardrail-action" class="text-sm font-light">Action</label>
					<select
						id="guardrail-action"
						class="select"
						bind:value={filter.action}
						disabled={readonly}
					>
						{#each actionOptions as option (option.value)}
							<option value={option.value}>{option.label}</option>
						{/each}
					</select>
					<p class="text-muted-content text-xs">
						{actionOptions.find((o) => o.value === filter.action)?.description}
					</p>
				</div>
				<div class="flex flex-col gap-2">
					<label for="guardrail-direction" class="text-sm font-light">Direction</label>
					<select
						id="guardrail-direction"
						class="select"
						bind:value={filter.direction}
						disabled={readonly}
					>
						{#each directionOptions as option (option.value)}
							<option value={option.value}>{option.label}</option>
						{/each}
					</select>
					<p class="text-muted-content text-xs">Which side of the tool call is checked.</p>
				</div>
				<div class="flex flex-col gap-2">
					<label for="guardrail-sampling-rate" class="text-sm font-light">Sampling rate (%)</label>
					<input
						id="guardrail-sampling-rate"
						type="number"
						min="1"
						max="100"
						bind:value={filter.samplingRate}
						class="text-input-filled dark:bg-base-100 {samplingRateError
							? 'border-error focus:border-error focus:ring-error'
							: ''}"
						disabled={readonly}
					/>
					{#if samplingRateError}
						<p class="text-xs text-error">Sampling rate must be between 1 and 100</p>
					{:else}
						<p class="text-muted-content text-xs">
							Percentage of matching traffic the filter checks.
						</p>
					{/if}
				</div>
				<div class="flex flex-col gap-1 md:pt-7">
					<Toggle
						classes={{
							label: 'text-sm gap-2 font-light text-base-content'
						}}
						label="Fail closed"
						labelInline
						checked={filter.failClosed}
						disabled={readonly}
						onChange={(checked) => (filter.failClosed = checked)}
					/>
					<p class="text-muted-content text-xs font-light">
						Block the call when a detector can't be evaluated.
					</p>
				</div>
			</div>

			{#if nonRedacting.length > 0}
				<div class="notification-alert flex items-start gap-2 rounded-md p-3 text-sm">
					<TriangleAlert class="size-4 shrink-0" />
					<span>
						{nonRedacting.map((d) => d.name).join(', ')} can't locate findings for redaction. Findings
						from {nonRedacting.length === 1 ? 'it' : 'them'} will block instead.
					</span>
				</div>
			{/if}

			{#if filter.action !== 'log'}
				<div class="flex flex-col gap-2">
					<label for="guardrail-block-message" class="text-sm font-light">
						Block Message (Optional)
					</label>
					<input
						id="guardrail-block-message"
						bind:value={filter.blockMessage}
						class="text-input-filled dark:bg-base-100"
						placeholder="Leave empty for the default message"
						disabled={readonly}
					/>
				</div>
			{/if}
		</div>

		<div class="flex flex-col gap-2">
			<div class="mb-2 flex md:flex-row flex-col md:items-center items-start gap-4 justify-between">
				<div class="flex flex-col gap-1">
					<h2 class="text-lg font-semibold">Users & Groups</h2>
					<p class="text-muted-content text-sm">
						Limit this filter to calls from these users and groups.
					</p>
				</div>
				{#if !readonly}
					<button
						class="btn btn-primary flex items-center gap-1 text-sm"
						onclick={() => searchUsersDialog?.open()}
					>
						<Plus class="size-4" /> Add User/Group
					</button>
				{/if}
			</div>
			<Table
				data={subjectRows}
				fields={['displayName', 'type']}
				headers={[{ property: 'displayName', title: 'Name' }]}
				noDataMessage="Not limited to anyone. The filter applies to everyone."
			>
				{#snippet actions(row)}
					{#if !readonly}
						<IconButton
							variant="danger"
							tooltip={{ text: 'Remove User/Group' }}
							onclick={() => (subjects = subjects.filter((s) => s.id !== row.id))}
						>
							<Trash2 class="size-4" />
						</IconButton>
					{/if}
				{/snippet}
			</Table>
		</div>

		<div class="flex flex-col gap-2">
			<div class="flex flex-col gap-1">
				<h2 class="text-lg font-semibold">Condition (Optional)</h2>
				<p class="text-muted-content text-sm">
					An optional <a
						href="https://cel.dev"
						target="_blank"
						rel="noopener noreferrer"
						class="text-link">CEL</a
					> expression. The filter only runs when it is true.
				</p>
			</div>
			<div
				class="dark:bg-base-200 dark:border-base-400 bg-base-100 flex flex-col gap-2 rounded-lg border border-transparent p-4"
			>
				<textarea
					id="guardrail-condition"
					aria-label="Condition"
					class="text-input-filled dark:bg-base-100 min-h-20 font-mono text-sm {conditionError
						? 'border-error focus:border-error focus:ring-error'
						: ''}"
					placeholder={CONDITION_EXAMPLE}
					bind:value={filter.condition}
					disabled={readonly}></textarea>
				{#if conditionError}
					<p class="text-xs text-error" role="alert">{conditionError}</p>
				{/if}
				<div class="text-muted-content grid grid-cols-1 gap-x-4 gap-y-1 text-xs md:grid-cols-2">
					{#each conditionVariables as v (v.name)}
						<span><code class="text-base-content">{v.name}</code> — {v.description}</span>
					{/each}
				</div>
			</div>
		</div>

		<div class="h-px bg-base-400 w-full my-4"></div>

		<SelectorsAndResourcesFormSegment bind:form={filter} {readonly} />

		{#if saveError}
			<div class="notification-error rounded-md p-3 text-sm">{saveError}</div>
		{/if}
	</div>
	{#if !readonly}
		<div
			class="bg-base-200 dark:bg-base-100 dark:text-muted-content sticky bottom-0 left-0 flex w-full justify-end gap-2 py-4 text-gray-400 z-50"
			out:fly={{ x: -100, duration }}
			in:fly={{ x: -100 }}
		>
			<div class="flex w-full justify-between gap-2">
				<div>
					{#if initialFilterId}
						<button
							class={twMerge('text-sm btn btn-soft', filter.disabled ? 'btn-primary' : 'btn-error')}
							disabled={saving}
							onclick={() => {
								filter.disabled = !filter.disabled;
								handleSave();
							}}
						>
							{#if saving}
								<Loading class="size-4" />
							{:else}
								{filter.disabled ? 'Enable' : 'Disable'} Filter
							{/if}
						</button>
					{/if}
				</div>
				<div class="flex gap-2">
					<button
						class="btn btn-secondary"
						onclick={() => {
							if (initialFilterId) {
								onUpdate?.(undefined);
							} else {
								onCreate?.(undefined);
							}
						}}
					>
						Cancel
					</button>
					<button
						id={MCP_FILTERS_FIELD_IDS.saveBtn}
						class="btn btn-primary text-sm"
						disabled={saving || validatingCondition || !!conditionError}
						onclick={handleSave}
					>
						{#if saving}
							<Loading class="size-4" />
						{:else}
							Save
						{/if}
					</button>
				</div>
			</div>
		</div>
	{/if}
</div>

<SearchUsers
	bind:this={searchUsersDialog}
	filterIds={subjects.map((s) => s.id)}
	onAdd={(users: OrgUser[], groups: OrgGroup[]) => {
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
