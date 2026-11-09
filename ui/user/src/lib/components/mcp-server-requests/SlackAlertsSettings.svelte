<script lang="ts">
	import { parseErrorContent } from '$lib/errors';
	import { m } from '$lib/i18n';
	import {
		getMCPServerRequestSettings,
		testMCPServerRequestSettings,
		updateMCPServerRequestSettings,
		type MCPServerRequestSettings
	} from '$lib/services/mcpserverrequests';
	import { profile } from '$lib/stores';
	import { success } from '$lib/stores/success';
	import { LoaderCircle, Lock, MessageSquare } from '@lucide/svelte';
	import { onMount } from 'svelte';

	// Where administrators are alerted about new requests: a Slack incoming webhook, set here
	// or, read-only, at deployment.

	let settings = $state<MCPServerRequestSettings>();
	let url = $state('');
	let editing = $state(false);
	let busy = $state<'save' | 'remove' | 'test'>();
	let error = $state('');

	let readonly = $derived(!!profile.current.isAdminReadonly?.());
	let locked = $derived(!!settings?.setViaDeployment || readonly);
	let showForm = $derived(!locked && (editing || !settings?.slackWebhookConfigured));

	onMount(async () => {
		try {
			settings = await getMCPServerRequestSettings({ dontLogErrors: true });
		} catch (e) {
			error = parseErrorContent(e).message;
		}
	});

	async function run(kind: 'save' | 'remove' | 'test', action: () => Promise<void>) {
		busy = kind;
		error = '';
		try {
			await action();
		} catch (e) {
			error = parseErrorContent(e).message;
		} finally {
			busy = undefined;
		}
	}

	function save() {
		return run('save', async () => {
			settings = await updateMCPServerRequestSettings(url.trim(), { dontLogErrors: true });
			url = '';
			editing = false;
			success.add(m.server_requests_slack_saved());
		});
	}

	function remove() {
		return run('remove', async () => {
			settings = await updateMCPServerRequestSettings('', { dontLogErrors: true });
			success.add(m.server_requests_slack_removed());
		});
	}

	function test() {
		return run('test', async () => {
			await testMCPServerRequestSettings({ dontLogErrors: true });
			success.add(m.server_requests_slack_test_sent());
		});
	}
</script>

<section
	class="dark:bg-base-200 dark:border-base-400 bg-base-100 flex flex-col gap-3 rounded-lg p-4 shadow-sm"
	aria-labelledby="slack-alerts-title"
>
	<div class="flex items-start gap-3">
		<MessageSquare class="text-primary mt-0.5 size-5 shrink-0" />
		<div class="flex flex-col gap-1">
			<h2 id="slack-alerts-title" class="font-semibold">{m.server_requests_slack_title()}</h2>
			<p class="text-muted-content text-sm">{m.server_requests_slack_description()}</p>
		</div>
	</div>

	{#if settings}
		{#if settings.slackWebhookConfigured}
			<p class="text-sm">
				{m.server_requests_slack_connected({ hint: settings.slackWebhookHint ?? '' })}
			</p>
		{:else}
			<p class="text-muted-content text-sm">{m.server_requests_slack_off()}</p>
		{/if}
		{#if settings.setViaDeployment}
			<p class="text-muted-content flex items-start gap-2 text-xs">
				<Lock class="mt-0.5 size-3.5 shrink-0" />
				{m.server_requests_slack_deployment()}
			</p>
		{/if}

		{#if showForm}
			<form
				class="flex flex-wrap items-end gap-2"
				onsubmit={(e) => {
					e.preventDefault();
					save();
				}}
			>
				<label class="flex min-w-64 flex-1 flex-col gap-1 text-sm">
					{m.server_requests_slack_url()}
					<input
						class="text-input-filled"
						type="password"
						autocomplete="off"
						placeholder="https://hooks.slack.com/services/…"
						bind:value={url}
					/>
				</label>
				<button type="submit" class="btn btn-primary" disabled={!url.trim() || !!busy}>
					{#if busy === 'save'}<LoaderCircle class="size-4 animate-spin" />{/if}
					{m.server_requests_slack_save()}
				</button>
				{#if editing}
					<button type="button" class="btn btn-secondary" onclick={() => (editing = false)}>
						{m.server_requests_slack_cancel()}
					</button>
				{/if}
			</form>
		{/if}

		{#if settings.slackWebhookConfigured && !showForm}
			<div class="flex flex-wrap gap-2">
				<button type="button" class="btn btn-secondary" disabled={!!busy} onclick={test}>
					{#if busy === 'test'}<LoaderCircle class="size-4 animate-spin" />{/if}
					{m.server_requests_slack_test()}
				</button>
				{#if !locked}
					<button type="button" class="btn btn-secondary" onclick={() => (editing = true)}>
						{m.server_requests_slack_replace()}
					</button>
					<button type="button" class="btn btn-secondary" disabled={!!busy} onclick={remove}>
						{m.server_requests_slack_remove()}
					</button>
				{/if}
			</div>
		{/if}
	{/if}

	{#if error}
		<p class="text-sm text-red-600" role="alert">{error}</p>
	{/if}
</section>
