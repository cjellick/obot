<script lang="ts">
	import { resolve } from '$app/paths';
	import Confirm from '$lib/components/Confirm.svelte';
	import Layout from '$lib/components/Layout.svelte';
	import FilterForm from '$lib/components/admin/FilterForm.svelte';
	import GuardrailFilterForm from '$lib/components/admin/GuardrailFilterForm.svelte';
	import AuditLogsPageContent from '$lib/components/admin/audit-logs/AuditLogsPageContent.svelte';
	import UsageGraphs from '$lib/components/admin/usage/UsageGraphs.svelte';
	import McpServerDetails from '$lib/components/mcp/McpServerDetails.svelte';
	import IconButton from '$lib/components/primitives/IconButton.svelte';
	import { VirtualPageViewport } from '$lib/components/ui/virtual-page';
	import { PAGE_TRANSITION_DURATION } from '$lib/constants.js';
	import { AdminService } from '$lib/services';
	import type { MCPFilterInput, SystemMCPServerCatalogEntry } from '$lib/services/admin/types';
	import { profile } from '$lib/stores';
	import { goto } from '$lib/url';
	import { BookOpenText, Info, Trash2 } from '@lucide/svelte';
	import type { Component } from 'svelte';
	import { fade } from 'svelte/transition';
	import { twMerge } from 'tailwind-merge';

	interface Props {
		title: string;
		filter?: MCPFilterInput;
		entry?: SystemMCPServerCatalogEntry;
		selected?: string;
		onSelectionChange?: (newSelection: string) => void;
	}

	let { title, filter, entry, onSelectionChange, selected = 'configuration' }: Props = $props();

	let deletingFilter = $state(false);

	// Guardrail filters run in-process, so there is no server to show details or usage for.
	const isGuardrail = $derived(!!filter?.guardrail);
	const tabs = $derived(
		isGuardrail
			? [
					{ label: 'Configuration', view: 'configuration' },
					{ label: 'Audit Logs', view: 'audit-logs' }
				]
			: [
					{ label: 'Configuration', view: 'configuration' },
					{ label: 'Server Details', view: 'server-details' },
					{ label: 'Audit Logs', view: 'audit-logs' },
					{ label: 'Usage', view: 'usage' }
				]
	);
	const currentView = $derived(
		tabs.some((tab) => tab.view === selected) ? selected : 'configuration'
	);

	const duration = PAGE_TRANSITION_DURATION;
	const mcpServerId = $derived(filter?.id ? `sms1${filter.id}` : undefined);
</script>

<Layout
	main={{
		component: VirtualPageViewport as unknown as Component,
		props: {
			class: '',
			as: 'main',
			itemHeight: 56,
			overscan: 5,
			disabled: currentView !== 'audit-logs'
		}
	}}
	{title}
	showBackButton
>
	<div class="h-full w-full flex flex-col gap-4" in:fade={{ duration }}>
		{#if filter?.id}
			<div class="flex w-full items-center justify-between gap-4">
				<h1 class="flex items-center gap-4 text-2xl font-semibold">
					{title || filter.name || 'Filter'}
				</h1>
				{#if !profile.current.isAdminReadonly?.() && !entry?.id}
					<IconButton
						variant="danger2"
						tooltip={{ text: 'Delete Filter', placement: 'left' }}
						onclick={() => (deletingFilter = true)}
					>
						<Trash2 class="size-4" />
					</IconButton>
				{/if}
			</div>
			<div class="flex flex-1 gap-2 py-1 text-sm font-light max-h-11.5">
				{#each tabs as tab (tab.view)}
					<button
						onclick={() => {
							onSelectionChange?.(tab.view);
						}}
						class={twMerge(
							'min-w-fit flex-1 rounded-md border border-transparent px-3 py-2 text-center whitespace-nowrap transition-colors duration-300',
							currentView === tab.view &&
								'dark:bg-base-200 dark:border-base-400 bg-base-100 shadow-sm',
							currentView !== tab.view && 'hover:bg-base-400'
						)}
					>
						{tab.label}
					</button>
				{/each}
			</div>

			{#if currentView === 'configuration' && isGuardrail}
				<GuardrailFilterForm
					{filter}
					onUpdate={() => {
						goto('/mcp-servers?view=filters', { invalidateAll: true });
					}}
					readonly={profile.current.isAdminReadonly?.()}
				/>
			{:else if currentView === 'configuration'}
				<FilterForm
					{filter}
					onUpdate={() => {
						goto('/mcp-servers?view=filters', { invalidateAll: true });
					}}
					readonly={profile.current.isAdminReadonly?.()}
					mcpSystemCatalogEntryId={entry?.id || filter.systemMCPServerCatalogEntryID}
				/>
			{:else if currentView === 'server-details'}
				<McpServerDetails
					entity="webhook-validation"
					entityId={filter.id}
					serverId={filter.id}
					readonly={profile.current.isAdminReadonly?.()}
					connectedUsers={[]}
					k8sOverrides={{
						title: 'Details',
						classes: {
							title: 'text-lg font-semibold'
						}
					}}
				/>
			{:else if currentView === 'audit-logs'}
				<div class="mt-4 flex flex-1 flex-col gap-8 pb-8">
					{#if isGuardrail}
						<div class="notification-info flex items-center gap-2 rounded-md p-3 text-sm">
							<Info class="size-4 shrink-0" />
							<span>
								This filter runs inside Obot, so its findings are recorded as guardrail events, not
								in these audit logs.
								<a href={resolve('/guardrails?view=events')} class="text-link"
									>View guardrail events</a
								>
							</span>
						</div>
					{/if}
					<AuditLogsPageContent mcpId={mcpServerId} mcpServerDisplayName={filter.name}>
						{#snippet emptyContent()}
							<div class="mt-12 flex w-md flex-col items-center gap-4 self-center text-center">
								<BookOpenText class="text-muted-content size-24 opacity-50" />
								<h4 class="text-muted-content text-lg font-semibold">No recent audit logs</h4>
								<p class="text-muted-content text-sm font-light">
									{isGuardrail
										? 'This filter has not had any active usage in the last 7 days.'
										: 'This web validation server has not had any active usage in the last 7 days.'}
								</p>
							</div>
						{/snippet}
					</AuditLogsPageContent>
				</div>
			{:else if currentView === 'usage'}
				<div class="mt-4 flex min-h-full flex-col gap-8 pb-8">
					<UsageGraphs mcpId={mcpServerId} mcpServerDisplayName={filter.name} />
				</div>
			{/if}
		{:else}
			<FilterForm
				{filter}
				onCreate={() => {
					goto('/mcp-servers?view=filters', { invalidateAll: true });
				}}
				readonly={profile.current.isAdminReadonly?.()}
				mcpSystemCatalogEntryId={entry?.id}
			/>
		{/if}
	</div>
</Layout>

<Confirm
	msg={`Delete ${filter?.name || 'this filter'}?`}
	show={deletingFilter}
	onsuccess={async () => {
		if (!filter?.id) return;
		await AdminService.deleteMCPFilter(filter.id);
		await goto('/mcp-servers?view=filters', { invalidateAll: true });
	}}
	oncancel={() => (deletingFilter = false)}
/>
