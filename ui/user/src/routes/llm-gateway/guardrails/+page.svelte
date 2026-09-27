<script lang="ts">
	import Confirm from '$lib/components/Confirm.svelte';
	import Layout from '$lib/components/Layout.svelte';
	import GuardrailsTabs from '$lib/components/llm-gateway/GuardrailsTabs.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import Table from '$lib/components/table/Table.svelte';
	import Loading from '$lib/icons/Loading.svelte';
	import { UserService } from '$lib/services';
	import {
		ACTION_LABELS,
		APPLY_TO_LABELS,
		deleteGuardrailRule,
		listGuardrailProfiles,
		listGuardrailRules,
		TARGET_LABELS,
		type GuardrailProfile,
		type GuardrailRule
	} from '$lib/services/llm-gateway/guardrails';
	import { profile } from '$lib/stores';
	import { goto } from '$lib/url';
	import { openUrl } from '$lib/utils';
	import { Plus, ShieldCheck, Trash2 } from '@lucide/svelte';
	import { onMount } from 'svelte';

	let rules = $state<GuardrailRule[]>([]);
	let profiles = $state<GuardrailProfile[]>([]);
	let userNames = $state<Record<string, string>>({});
	let loading = $state(true);
	let toDelete = $state<GuardrailRule>();

	let readonly = $derived(profile.current.isAdminReadonly?.());

	async function load() {
		loading = true;
		try {
			[rules, profiles] = await Promise.all([listGuardrailRules(), listGuardrailProfiles()]);
			if (rules.some((r) => r.subjects?.some((s) => s.type === 'user'))) {
				const users = await UserService.listUsers().catch(() => []);
				userNames = Object.fromEntries(
					users.map((u) => [u.id, u.displayName || u.email || u.username || u.id])
				);
			}
		} finally {
			loading = false;
		}
	}

	onMount(load);

	function assigned(r: GuardrailRule) {
		if (!r.subjects?.length || r.subjects.some((s) => s.type === 'everyone')) return 'Everyone';
		return r.subjects
			.map((s) => (s.type === 'user' ? (userNames[s.id ?? ''] ?? s.id) : `Group: ${s.id}`))
			.join(', ');
	}

	let profileNames = $derived(Object.fromEntries(profiles.map((p) => [p.id, p.name])));

	let rows = $derived(
		rules.map((r) => ({
			...r,
			id: r.id ?? r.name,
			targetLabel: TARGET_LABELS[r.target] ?? r.target,
			stageLabel: APPLY_TO_LABELS[r.applyTo] ?? r.applyTo,
			actionLabel: ACTION_LABELS[r.action] ?? r.action,
			profilesLabel: (r.profileIDs ?? []).map((id) => profileNames[id] ?? id).join(', '),
			assignedLabel: assigned(r),
			statusLabel: r.enabled ? 'Enabled' : 'Disabled'
		}))
	);
</script>

<svelte:head>
	<title>Obot | Guardrails</title>
</svelte:head>

<Layout title="Guardrails" classes={{ childrenContainer: 'max-w-none' }}>
	{#snippet rightNavActions()}
		{#if !readonly}
			<button
				class="btn btn-primary flex items-center gap-1 text-sm"
				onclick={() => goto('/llm-gateway/guardrails/rules/new')}
			>
				<Plus class="size-4" /> Add Rule
			</button>
		{/if}
	{/snippet}

	<div class="flex flex-col gap-6">
		<p class="text-muted-content max-w-4xl text-sm">
			Guardrails check LLM gateway requests and responses, including streamed responses, and MCP
			tool call arguments and results. Each rule picks the traffic to check and one or more profiles
			to check it with, and blocks, redacts or only logs what they find. Rules run in the order they
			were created. Message policies are now LLM Judge profiles.
		</p>

		<GuardrailsTabs active="rules" />

		{#if loading}
			<div class="flex justify-center p-12"><Loading class="size-8" /></div>
		{:else if rules.length === 0}
			<div class="mt-12 flex w-md flex-col items-center gap-4 self-center text-center">
				<ShieldCheck class="text-base-content/80 size-24 opacity-25" />
				<h4 class="text-muted-content text-lg font-semibold">No guardrail rules</h4>
				<p class="text-muted-content text-sm font-light">
					Without a rule, no traffic is checked. Create a profile, then add a rule that applies it
					to LLM or MCP traffic.
				</p>
			</div>
		{:else}
			<Table
				data={rows}
				fields={[
					'name',
					'targetLabel',
					'stageLabel',
					'actionLabel',
					'profilesLabel',
					'assignedLabel',
					'statusLabel'
				]}
				headers={[
					{ property: 'name', title: 'Name' },
					{ property: 'targetLabel', title: 'Target' },
					{ property: 'stageLabel', title: 'Stage' },
					{ property: 'actionLabel', title: 'Action' },
					{ property: 'profilesLabel', title: 'Profiles' },
					{ property: 'assignedLabel', title: 'Assigned to' },
					{ property: 'statusLabel', title: 'Status' }
				]}
				filterable={['targetLabel', 'actionLabel', 'statusLabel']}
				onClickRow={(row, isCtrl) => openUrl(`/llm-gateway/guardrails/rules/${row.id}`, isCtrl)}
			>
				{#snippet actions(row)}
					{#if !readonly}
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

<Confirm
	show={!!toDelete}
	msg={`Delete ${toDelete?.name}? It stops applying immediately.`}
	onsuccess={async () => {
		if (toDelete?.id) await deleteGuardrailRule(toDelete.id);
		toDelete = undefined;
		await load();
	}}
	oncancel={() => (toDelete = undefined)}
/>
