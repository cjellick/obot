<script lang="ts">
	import { parseErrorContent } from '$lib/errors';
	import { m } from '$lib/i18n';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		CUSTOM_LABEL_NAME_PATTERN,
		createCustomLabel,
		isBuiltinLabel,
		testCustomLabel,
		updateCustomLabel,
		type CustomLabel,
		type CustomLabelTestResult,
		type LabelPatternType,
		type LabelResourceSummary,
		type LabelSourceType
	} from '$lib/services/provenance';
	import { Plus, Trash2 } from '@lucide/svelte';
	import { untrack } from 'svelte';

	// Creates or edits an administrator-defined label: its name, the plain-English description
	// the AI tagger follows, and its pattern and source detectors.

	interface Props {
		/** The label to edit; omit to create one. */
		label?: CustomLabel;
		/** MCP servers and vMCPs offered as sources. */
		resources?: LabelResourceSummary[];
		/** Names already in use, to reject a duplicate before saving. */
		existing?: string[];
		onsaved: (label: CustomLabel) => void;
		oncancel: () => void;
	}

	let { label, resources = [], existing = [], onsaved, oncancel }: Props = $props();

	// The editor works on a copy taken when it opens.
	const initial = untrack(() => label);
	const editing = !!initial;
	let draft = $state<CustomLabel>(
		initial
			? structuredClone($state.snapshot(initial))
			: { name: '', description: '', ai: false, confidential: true, patterns: [], sources: [] }
	);
	let saving = $state(false);
	let error = $state('');
	let sample = $state('');
	let testing = $state(false);
	let testResult = $state<CustomLabelTestResult>();

	let nameError = $derived.by(() => {
		const name = draft.name.trim();
		if (!name || editing) return '';
		if (!CUSTOM_LABEL_NAME_PATTERN.test(name)) return m.supervision_custom_labels_name_invalid();
		if (isBuiltinLabel(name)) return m.supervision_custom_labels_name_builtin();
		if (existing.includes(name)) return m.supervision_custom_labels_name_taken();
		return '';
	});
	let hasDetector = $derived(
		draft.ai || draft.patterns.some((p) => p.value.trim()) || draft.sources.some((s) => s.id.trim())
	);
	let canSave = $derived(
		!saving && !!draft.name.trim() && !nameError && !!draft.description.trim() && hasDetector
	);
	let prefix = $derived(editing ? `custom-label-${draft.name}` : 'custom-label-new');

	const patternTypes: { value: LabelPatternType; label: () => string }[] = [
		{ value: 'keyword', label: m.supervision_custom_labels_pattern_keyword },
		{ value: 'regex', label: m.supervision_custom_labels_pattern_regex }
	];
	const sourceTypes: { value: LabelSourceType; label: () => string }[] = [
		{ value: 'catalog-entry', label: m.supervision_custom_labels_source_mcp },
		{ value: 'vmcp', label: m.supervision_custom_labels_source_vmcp },
		{ value: 'host', label: m.supervision_custom_labels_source_host }
	];

	function resourcesOf(type: LabelSourceType) {
		return resources.filter((r) => r.resourceType === type);
	}
	function sourceName(type: LabelSourceType, id: string) {
		const match = resources.find((r) => r.resourceType === type && r.resourceID === id);
		return match?.displayName || undefined;
	}
	function cleaned(): CustomLabel {
		return {
			...draft,
			name: draft.name.trim(),
			description: draft.description.trim(),
			patterns: draft.patterns
				.filter((p) => p.value.trim())
				.map((p) => ({ ...p, value: p.type === 'keyword' ? p.value.trim() : p.value })),
			sources: draft.sources
				.filter((s) => s.id.trim())
				.map((s) => ({
					type: s.type,
					id: s.id.trim(),
					tool: s.type === 'host' ? undefined : s.tool?.trim() || undefined,
					name: s.type === 'host' ? undefined : sourceName(s.type, s.id.trim())
				}))
		};
	}
	async function save() {
		if (!canSave) return;
		saving = true;
		error = '';
		try {
			const value = cleaned();
			const saved = editing
				? await updateCustomLabel(value, { dontLogErrors: true })
				: await createCustomLabel(value, { dontLogErrors: true });
			onsaved(saved);
		} catch (e) {
			error = parseErrorContent(e).message;
		} finally {
			saving = false;
		}
	}
	async function runTest() {
		testing = true;
		error = '';
		testResult = undefined;
		try {
			testResult = await testCustomLabel(cleaned(), sample, { dontLogErrors: true });
		} catch (e) {
			error = parseErrorContent(e).message;
		} finally {
			testing = false;
		}
	}
</script>

<form
	class="paper gap-4"
	aria-label={editing
		? m.supervision_custom_labels_edit_named({ name: draft.name })
		: m.supervision_custom_labels_new()}
	onsubmit={(event) => {
		event.preventDefault();
		void save();
	}}
>
	<h2 class="text-base font-semibold">
		{editing
			? m.supervision_custom_labels_edit_named({ name: draft.name })
			: m.supervision_custom_labels_new()}
	</h2>

	<div class="grid gap-4 md:grid-cols-[16rem_1fr]">
		<div class="flex flex-col gap-1">
			<label for={`${prefix}-name`} class="text-sm font-light"
				>{m.supervision_custom_labels_name()}</label
			>
			<input
				id={`${prefix}-name`}
				class="text-input-filled font-mono text-sm"
				placeholder="project-atlas"
				autocomplete="off"
				disabled={editing}
				bind:value={draft.name}
			/>
			{#if nameError}<span class="text-error text-xs" role="alert">{nameError}</span>
			{:else}<span class="text-muted-content text-xs"
					>{editing
						? m.supervision_custom_labels_name_fixed()
						: m.supervision_custom_labels_name_help()}</span
				>{/if}
		</div>
		<div class="flex flex-col gap-1">
			<label for={`${prefix}-description`} class="text-sm font-light"
				>{m.supervision_custom_labels_description()}</label
			>
			<textarea
				id={`${prefix}-description`}
				class="text-input-filled min-h-20 text-sm"
				placeholder={m.supervision_custom_labels_description_placeholder()}
				bind:value={draft.description}></textarea>
			<span class="text-muted-content text-xs"
				>{m.supervision_custom_labels_description_help()}</span
			>
		</div>
	</div>

	<fieldset class="flex flex-col gap-3">
		<legend class="mb-2 text-sm font-semibold">{m.supervision_custom_labels_detectors()}</legend>

		<label class="flex items-start gap-2 text-sm">
			<input type="checkbox" class="checkbox checkbox-sm mt-0.5" bind:checked={draft.ai} />
			<span>
				<span class="font-medium">{m.supervision_custom_labels_ai()}</span>
				<span class="text-muted-content block text-xs">{m.supervision_custom_labels_ai_help()}</span
				>
			</span>
		</label>

		<section class="flex flex-col gap-2" aria-label={m.supervision_custom_labels_patterns()}>
			<div>
				<h3 class="text-sm font-medium">{m.supervision_custom_labels_patterns()}</h3>
				<p class="text-muted-content text-xs">{m.supervision_custom_labels_patterns_help()}</p>
			</div>
			{#each draft.patterns as pattern, i (i)}
				<div class="flex flex-wrap items-center gap-2">
					<select
						class="select select-sm w-36"
						aria-label={m.supervision_custom_labels_pattern_type()}
						bind:value={pattern.type}
					>
						{#each patternTypes as option (option.value)}<option value={option.value}
								>{option.label()}</option
							>{/each}
					</select>
					<input
						class="text-input-filled min-w-48 flex-1 font-mono text-sm"
						aria-label={m.supervision_custom_labels_pattern_value()}
						placeholder={pattern.type === 'regex' ? '\\bATL-\\d{4}\\b' : 'Project Atlas'}
						bind:value={pattern.value}
					/>
					<button
						type="button"
						class="btn btn-ghost btn-sm btn-square"
						aria-label={m.supervision_custom_labels_remove_pattern()}
						onclick={() => draft.patterns.splice(i, 1)}><Trash2 class="size-4" /></button
					>
				</div>
			{/each}
			<button
				type="button"
				class="btn btn-secondary btn-sm w-fit"
				onclick={() => draft.patterns.push({ type: 'keyword', value: '' })}
				><Plus class="size-4" />{m.supervision_custom_labels_add_pattern()}</button
			>
		</section>

		<section class="flex flex-col gap-2" aria-label={m.supervision_custom_labels_sources()}>
			<div>
				<h3 class="text-sm font-medium">{m.supervision_custom_labels_sources()}</h3>
				<p class="text-muted-content text-xs">{m.supervision_custom_labels_sources_help()}</p>
			</div>
			{#each draft.sources as source, i (i)}
				<div class="flex flex-wrap items-center gap-2">
					<select
						class="select select-sm w-36"
						aria-label={m.supervision_custom_labels_source_type()}
						bind:value={source.type}
					>
						{#each sourceTypes as option (option.value)}<option value={option.value}
								>{option.label()}</option
							>{/each}
					</select>
					<input
						class="text-input-filled min-w-48 flex-1 font-mono text-sm"
						aria-label={source.type === 'host'
							? m.supervision_custom_labels_source_host_value()
							: m.supervision_custom_labels_source_id()}
						placeholder={source.type === 'host' ? '*.partner.example' : ''}
						list={source.type === 'host' ? undefined : `${prefix}-resources-${source.type}`}
						bind:value={source.id}
					/>
					{#if source.type !== 'host'}<input
							class="text-input-filled w-44 font-mono text-sm"
							aria-label={m.supervision_custom_labels_source_tool()}
							placeholder={m.supervision_custom_labels_source_tool_placeholder()}
							bind:value={source.tool}
						/>{/if}
					<button
						type="button"
						class="btn btn-ghost btn-sm btn-square"
						aria-label={m.supervision_custom_labels_remove_source()}
						onclick={() => draft.sources.splice(i, 1)}><Trash2 class="size-4" /></button
					>
				</div>
			{/each}
			{#each ['catalog-entry', 'vmcp'] as const as type (type)}
				<datalist id={`${prefix}-resources-${type}`}>
					{#each resourcesOf(type) as resource (resource.resourceID)}<option
							value={resource.resourceID}>{resource.displayName}</option
						>{/each}
				</datalist>
			{/each}
			<button
				type="button"
				class="btn btn-secondary btn-sm w-fit"
				onclick={() => draft.sources.push({ type: 'catalog-entry', id: '', tool: '' })}
				><Plus class="size-4" />{m.supervision_custom_labels_add_source()}</button
			>
		</section>
		{#if !hasDetector}<p class="text-warning text-xs">
				{m.supervision_custom_labels_need_detector()}
			</p>{/if}
	</fieldset>

	<label class="flex items-start gap-2 text-sm">
		<input type="checkbox" class="checkbox checkbox-sm mt-0.5" bind:checked={draft.confidential} />
		<span>
			<span class="font-medium">{m.supervision_custom_labels_confidential()}</span>
			<span class="text-muted-content block text-xs"
				>{m.supervision_custom_labels_confidential_help()}</span
			>
		</span>
	</label>

	{#if draft.patterns.length}
		<details class="text-sm">
			<summary class="cursor-pointer font-medium">{m.supervision_custom_labels_test()}</summary>
			<div class="mt-2 flex flex-col gap-2">
				<textarea
					class="text-input-filled min-h-16 text-sm"
					aria-label={m.supervision_custom_labels_test_sample()}
					placeholder={m.supervision_custom_labels_test_sample()}
					bind:value={sample}></textarea>
				<button
					type="button"
					class="btn btn-secondary btn-sm w-fit"
					disabled={testing || !sample.trim()}
					onclick={runTest}
					>{#if testing}<Loading
							class="size-4"
						/>{/if}{m.supervision_custom_labels_test_run()}</button
				>
				{#if testResult}
					<div role="status" class="text-xs">
						{#if testResult.matched}
							<p class="text-success font-medium">{m.supervision_custom_labels_test_matched()}</p>
							<ul class="text-muted-content mt-1 list-disc pl-5">
								{#each testResult.hits as hit, i (i)}<li>
										{hit.detail}{#if hit.matches?.length}: <code>{hit.matches.join(', ')}</code
											>{/if}
									</li>{/each}
							</ul>
						{:else}
							<p class="text-muted-content">{m.supervision_custom_labels_test_no_match()}</p>
						{/if}
					</div>
				{/if}
			</div>
		</details>
	{/if}

	{#if error}<p class="notification-error rounded-md p-3 text-sm" role="alert">{error}</p>{/if}

	<div class="flex flex-wrap justify-end gap-2">
		<button type="button" class="btn btn-secondary btn-sm" onclick={oncancel}
			>{m.common_cancel()}</button
		>
		<button type="submit" class="btn btn-primary btn-sm" disabled={!canSave}
			>{#if saving}<Loading class="size-4" />{/if}{editing
				? m.supervision_custom_labels_save()
				: m.supervision_custom_labels_create()}</button
		>
	</div>
</form>
