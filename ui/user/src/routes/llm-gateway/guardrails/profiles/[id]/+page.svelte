<script lang="ts">
	import { page } from '$app/state';
	import Layout from '$lib/components/Layout.svelte';
	import SensitiveInput from '$lib/components/SensitiveInput.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import { parseErrorContent } from '$lib/errors';
	import Loading from '$lib/icons/Loading.svelte';
	import {
		createGuardrailProfile,
		getGuardrailProfile,
		listGuardrailProviders,
		testGuardrailProfile,
		updateGuardrailProfile,
		type GuardrailField,
		type GuardrailProvider,
		type GuardrailStage,
		type GuardrailTarget,
		type GuardrailTestResult
	} from '$lib/services/llm-gateway/guardrails';
	import { profile } from '$lib/stores';
	import { success } from '$lib/stores/success';
	import { goto } from '$lib/url';
	import { onMount } from 'svelte';

	// A new profile's ID once it is saved; the page stays open so it can be tested.
	let createdID = $state<string>();
	let routeID = $derived(page.params.id ?? 'new');
	let profileID = $derived(createdID ?? (routeID === 'new' ? undefined : routeID));
	let isNew = $derived(!profileID);

	let loading = $state(true);
	let saving = $state(false);
	let error = $state('');
	let loadError = $state('');

	let provider = $state<GuardrailProvider>();
	let name = $state('');
	let description = $state('');
	let values = $state<Record<string, unknown>>({});
	// List fields are edited as one entry per line.
	let listText = $state<Record<string, string>>({});
	let secrets = $state<Record<string, string>>({});
	let configuredSecrets = $state<string[]>([]);
	let profileError = $state('');

	let testTarget = $state<GuardrailTarget>('llm');
	let testStage = $state<GuardrailStage>('input');
	let testText = $state('');
	let testing = $state(false);
	let testResult = $state<GuardrailTestResult>();
	let testError = $state('');

	let readonly = $derived(profile.current.isAdminReadonly?.());
	// Testing needs a saved profile, and auditors can't run tests.
	let testDisabled = $derived(isNew || !!readonly);

	function applyConfig(fields: GuardrailField[], config: Record<string, unknown>) {
		const next: Record<string, unknown> = {};
		const nextLists: Record<string, string> = {};
		for (const f of fields) {
			if (f.sensitive) continue;
			const v = config[f.name] ?? f.default;
			if (f.type === 'list' && !f.options?.length) {
				nextLists[f.name] = Array.isArray(v) ? v.join('\n') : typeof v === 'string' ? v : '';
			} else if (f.type === 'list') {
				next[f.name] = Array.isArray(v) ? v : [];
			} else if (f.type === 'bool') {
				next[f.name] = v === true || v === 'true';
			} else {
				next[f.name] = v ?? '';
			}
		}
		values = next;
		listText = nextLists;
	}

	onMount(async () => {
		try {
			const providers = await listGuardrailProviders();
			if (isNew) {
				const type = page.url.searchParams.get('type') ?? '';
				provider = providers.find((p) => p.type === type);
				if (!provider) {
					loadError = type ? `Unknown profile type "${type}".` : 'No profile type selected.';
					return;
				}
				name = provider.label;
				applyConfig(provider.fields, {});
				return;
			}
			const p = await getGuardrailProfile(profileID!);
			provider = providers.find((x) => x.type === p.type);
			name = p.name;
			description = p.description ?? '';
			configuredSecrets = p.configuredSecrets ?? [];
			profileError = p.error ?? '';
			if (!provider) {
				loadError = `Unknown profile type "${p.type}".`;
				return;
			}
			applyConfig(provider.fields, p.config ?? {});
		} finally {
			loading = false;
		}
	});

	let missingRequired = $derived(
		(provider?.fields ?? []).some((f) => {
			if (!f.required) return false;
			if (f.sensitive) return !secrets[f.name]?.trim() && !configuredSecrets.includes(f.name);
			if (f.type === 'list') {
				return f.options?.length
					? !(values[f.name] as string[])?.length
					: !listText[f.name]?.trim();
			}
			const v = values[f.name];
			return v === undefined || v === null || v === '';
		})
	);

	let valid = $derived(!!provider && name.trim().length > 0 && !missingRequired);

	function buildConfig() {
		const config: Record<string, unknown> = {};
		for (const f of provider?.fields ?? []) {
			if (f.sensitive) {
				const v = secrets[f.name]?.trim();
				if (v) config[f.name] = v;
				continue;
			}
			if (f.type === 'list' && !f.options?.length) {
				const items = (listText[f.name] ?? '')
					.split('\n')
					.map((s) => s.trim())
					.filter(Boolean);
				if (items.length) config[f.name] = items;
				continue;
			}
			const v = values[f.name];
			if (v === undefined || v === null || v === '') continue;
			config[f.name] = f.type === 'number' ? Number(v) : v;
		}
		return config;
	}

	async function save() {
		if (!provider) return;
		saving = true;
		error = '';
		try {
			const body = {
				name: name.trim(),
				description,
				type: provider.type,
				config: buildConfig()
			};
			const saved = profileID
				? await updateGuardrailProfile(profileID, body, { dontLogErrors: true })
				: await createGuardrailProfile(body, { dontLogErrors: true });
			configuredSecrets = saved.configuredSecrets ?? [];
			profileError = saved.error ?? '';
			secrets = {};
			success.add(profileID ? 'Guardrail profile saved.' : 'Guardrail profile created.');
			if (!profileID && saved.id) {
				createdID = saved.id;
				goto(`/llm-gateway/guardrails/profiles/${saved.id}`, { replaceState: true });
			}
		} catch (e) {
			error = parseErrorContent(e).message;
		} finally {
			saving = false;
		}
	}

	async function runTest() {
		if (!profileID) return;
		testing = true;
		testError = '';
		testResult = undefined;
		try {
			testResult = await testGuardrailProfile(
				profileID,
				{ target: testTarget, stage: testStage, text: testText },
				{ dontLogErrors: true }
			);
		} catch (e) {
			testError = parseErrorContent(e).message;
		} finally {
			testing = false;
		}
	}

	function toggleOption(field: string, option: string, checked: boolean) {
		const current = (values[field] as string[]) ?? [];
		values[field] = checked ? [...current, option] : current.filter((o) => o !== option);
	}

	let title = $derived(
		isNew ? `New ${provider?.label ?? 'Guardrail'} Profile` : name || 'Guardrail Profile'
	);
</script>

<svelte:head>
	<title>Obot | {title}</title>
</svelte:head>

<Layout
	{title}
	showBackButton
	onBackButtonClick={() => goto('/llm-gateway/guardrails/profiles')}
	classes={{ childrenContainer: 'max-w-5xl' }}
>
	{#if loading && !loadError}
		<div class="flex justify-center p-12"><Loading class="size-8" /></div>
	{:else if loadError || !provider}
		<div class="notification-error rounded-md p-3 text-sm">{loadError}</div>
	{:else}
		<div class="flex flex-col gap-8 pb-24">
			<section class="paper flex flex-col gap-4 p-4">
				<div>
					<span class="text-xs font-semibold uppercase">{provider.label}</span>
					<p class="text-muted-content text-xs">{provider.description}</p>
				</div>
				<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
					<div class="flex flex-col gap-1">
						<label for="profile-name" class="text-sm font-light">Name</label>
						<input
							id="profile-name"
							class="text-input-filled"
							bind:value={name}
							disabled={readonly}
						/>
					</div>
					<div class="flex flex-col gap-1">
						<label for="profile-description" class="text-sm font-light">Description</label>
						<input
							id="profile-description"
							class="text-input-filled"
							bind:value={description}
							disabled={readonly}
						/>
					</div>
				</div>
				{#if profileError}
					<div class="notification-alert rounded-md p-3 text-sm">{profileError}</div>
				{/if}
			</section>

			<section class="flex flex-col gap-2">
				<h2 class="text-lg font-semibold">Settings</h2>
				<div class="paper flex flex-col gap-4 p-4">
					{#each provider.fields as field (field.name)}
						{@const inputID = `field-${field.name}`}
						<div class="flex flex-col gap-1">
							{#if field.type === 'bool'}
								<Toggle
									label={field.label}
									labelInline
									checked={!!values[field.name]}
									disabled={readonly}
									onChange={(v) => (values[field.name] = v)}
								/>
							{:else}
								<label for={inputID} class="text-sm font-light">
									{field.label}
									{#if field.required}<span class="text-error">*</span>{/if}
								</label>
								{#if field.sensitive}
									<SensitiveInput
										name={inputID}
										textarea={field.type === 'textarea'}
										bind:value={() => secrets[field.name] ?? '', (v) => (secrets[field.name] = v)}
										placeholder={configuredSecrets.includes(field.name)
											? 'Configured — leave blank to keep'
											: undefined}
										disabled={readonly}
									/>
								{:else if field.type === 'textarea'}
									<textarea
										id={inputID}
										class="text-input-filled min-h-24 text-sm"
										bind:value={
											() => String(values[field.name] ?? ''), (v) => (values[field.name] = v)
										}
										disabled={readonly}></textarea>
								{:else if field.type === 'number'}
									<input
										id={inputID}
										type="number"
										step="any"
										class="text-input-filled w-40"
										bind:value={values[field.name]}
										disabled={readonly}
									/>
								{:else if field.type === 'select'}
									<select
										id={inputID}
										class="select w-fit"
										bind:value={values[field.name]}
										disabled={readonly}
									>
										{#if !field.required}<option value="">Default</option>{/if}
										{#each field.options ?? [] as option (option)}
											<option value={option}>{option}</option>
										{/each}
									</select>
								{:else if field.type === 'list' && field.options?.length}
									<div id={inputID} class="flex flex-wrap gap-x-4 gap-y-1">
										{#each field.options as option (option)}
											<label class="flex items-center gap-2 text-sm">
												<input
													type="checkbox"
													class="checkbox checkbox-sm"
													checked={((values[field.name] as string[]) ?? []).includes(option)}
													disabled={readonly}
													onchange={(e) =>
														toggleOption(
															field.name,
															option,
															(e.target as HTMLInputElement).checked
														)}
												/>
												<span class="font-mono text-xs">{option}</span>
											</label>
										{/each}
									</div>
								{:else if field.type === 'list'}
									<textarea
										id={inputID}
										class="text-input-filled min-h-20 font-mono text-sm"
										placeholder="One per line"
										bind:value={listText[field.name]}
										disabled={readonly}></textarea>
								{:else}
									<input
										id={inputID}
										class="text-input-filled"
										bind:value={
											() => String(values[field.name] ?? ''), (v) => (values[field.name] = v)
										}
										placeholder={typeof field.default === 'string' ? field.default : undefined}
										disabled={readonly}
									/>
								{/if}
							{/if}
							{#if field.description}
								<span class="text-muted-content text-xs">{field.description}</span>
							{/if}
						</div>
					{:else}
						<span class="text-muted-content text-sm">This profile type has no settings.</span>
					{/each}
				</div>
			</section>

			<section class="flex flex-col gap-2">
				<div>
					<h2 class="text-lg font-semibold">Test</h2>
					<p class="text-muted-content text-xs">
						{isNew
							? 'Save the profile to test it.'
							: 'Runs the saved settings against sample text. Save changes before testing them.'}
					</p>
				</div>
				<div class="paper flex flex-col gap-3 p-4">
					<div class="flex flex-wrap gap-3">
						<div class="flex flex-col gap-1">
							<label for="test-target" class="text-sm font-light">Target</label>
							<select
								id="test-target"
								class="select"
								bind:value={testTarget}
								disabled={testDisabled}
							>
								<option value="llm">LLM</option>
								<option value="mcp">MCP</option>
							</select>
						</div>
						<div class="flex flex-col gap-1">
							<label for="test-stage" class="text-sm font-light">Stage</label>
							<select id="test-stage" class="select" bind:value={testStage} disabled={testDisabled}>
								<option value="input">Input</option>
								<option value="output">Output</option>
							</select>
						</div>
					</div>
					<div class="flex flex-col gap-1">
						<label for="test-text" class="text-sm font-light">Sample text</label>
						<textarea
							id="test-text"
							class="text-input-filled min-h-24 text-sm"
							bind:value={testText}
							disabled={testDisabled}></textarea>
					</div>
					<button
						class="btn btn-secondary w-fit text-sm"
						disabled={testDisabled || testing || !testText.trim()}
						onclick={runTest}
					>
						{#if testing}<Loading class="size-4" />{:else}Run test{/if}
					</button>
					{#if testError || testResult?.error}
						<div class="notification-error rounded-md p-3 text-sm">
							{testError || testResult?.error}
						</div>
					{:else if testResult}
						{#if testResult.findings.length === 0}
							<span class="text-success text-sm">No findings.</span>
						{:else}
							<div class="flex flex-col gap-1">
								<span class="text-sm font-medium">
									{testResult.findings.length}
									{testResult.findings.length === 1 ? 'finding' : 'findings'}
								</span>
								<ul class="flex flex-col gap-1 text-sm">
									{#each testResult.findings as f, i (i)}
										<li>
											<span class="bg-error/15 text-error rounded-full px-2 py-0.5 text-xs"
												>{f.category}</span
											>
											{#if f.count && f.count > 1}<span class="text-xs">×{f.count}</span>{/if}
											{#if f.message}<span class="text-muted-content text-xs">{f.message}</span
												>{/if}
										</li>
									{/each}
								</ul>
							</div>
							{#if testResult.redactedText}
								<div class="flex flex-col gap-1">
									<span class="text-sm font-medium">Redacted text</span>
									<pre
										class="bg-base-200 dark:bg-base-300 rounded-md p-3 text-xs whitespace-pre-wrap">{testResult.redactedText}</pre>
								</div>
							{:else if !testResult.canRedact}
								<span class="text-muted-content text-xs">
									This profile can't locate findings, so redacting rules block instead.
								</span>
							{/if}
						{/if}
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
				<button
					class="btn btn-secondary text-sm"
					onclick={() => goto('/llm-gateway/guardrails/profiles')}>Cancel</button
				>
				<button class="btn btn-primary text-sm" disabled={!valid || saving} onclick={save}>
					{#if saving}<Loading class="size-4" />{:else}Save{/if}
				</button>
			</div>
		{/if}
	{/if}
</Layout>
