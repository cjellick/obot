<script lang="ts">
	import ResponsiveDialog from '$lib/components/ResponsiveDialog.svelte';
	import ProviderDeconfigureConfirm from '$lib/components/admin/ProviderDeconfigureConfirm.svelte';
	import { reloadPage } from '$lib/navigation';
	import { AdminService, type AuthProvider } from '$lib/services';
	import { version, license, darkMode } from '$lib/stores';
	import { adminConfigStore } from '$lib/stores/adminConfig.svelte';
	import LicenseProviderDialog from './LicenseProviderDialog.svelte';
	import { KeyRound, Mail } from '@lucide/svelte';
	import { slide } from 'svelte/transition';

	interface Props {
		warnUserLimit?: boolean;
	}

	const { warnUserLimit }: Props = $props();

	let licenseViolationDialog = $state<ReturnType<typeof ResponsiveDialog>>();
	let confirmDowngradeDialog = $state<ReturnType<typeof ProviderDeconfigureConfirm>>();
	let providersToDeconfigure = $state<AuthProvider[]>([]);

	let licenseRequiredProvider = $state<AuthProvider>();

	let downgrading = $state(false);
	let error = $state('');
	let violations = $derived(
		(version.current.licenseEntitlementViolations || []).reduce(
			(arr, violation) => {
				if (violation.type === 'authProvider') {
					arr.authProvider = true;
				} else if (violation.type === 'modelProvider') {
					arr.modelProvider = true;
				} else if (violation.type === 'userLimit') {
					arr.userLimit = true;
				}
				return arr;
			},
			{
				authProvider: false,
				modelProvider: false,
				userLimit: false
			}
		)
	);

	export function open() {
		licenseViolationDialog?.open();
	}
	export function close() {
		licenseViolationDialog?.close();
	}

	async function handleDowngrade() {
		if (!version.current.licenseEntitlementViolations) {
			console.error('No license entitlement violations found');
			return;
		}

		downgrading = true;
		try {
			// Model providers are served by the LLM gateway and can't be deconfigured here, so only
			// auth providers are downgraded.
			const authProviderViolations = version.current.licenseEntitlementViolations.filter(
				(violation) => violation.type === 'authProvider'
			);
			for (const authProvider of authProviderViolations) {
				await AdminService.deconfigureAuthProvider(authProvider.name);
			}

			reloadPage();
		} catch (err) {
			error = err instanceof Error ? err.message : 'An unknown error occurred.';
		} finally {
			confirmDowngradeDialog?.close();
			downgrading = false;
		}
	}
</script>

<ResponsiveDialog
	bind:this={licenseViolationDialog}
	title={warnUserLimit || violations.userLimit
		? warnUserLimit
			? 'Nearing User Limit'
			: 'User Limit Reached'
		: 'Missing or Invalid License'}
	class="md:max-w-md"
>
	<div class="md:p-0 p-4">
		<div class="flex flex-col gap-4">
			<p class="font-light text-center">
				{#if warnUserLimit || violations.userLimit}
					Unlock unlimited users and get support with Obot Enterprise! Contact us at <a
						href="mailto:info@obot.ai"
						class="text-link">info@obot.ai</a
					> to upgrade.
				{:else}
					To re-enable access to existing functionality,
					{#if violations.authProvider}
						register your email below or
					{/if}
					contact support at
					<a href="mailto:info@obot.ai" class="text-link">info@obot.ai</a> to renew an Enterprise license.
				{/if}
			</p>
			{#if violations.authProvider}
				<button
					class="btn btn-primary"
					onclick={() => {
						const violationId = version.current.licenseEntitlementViolations?.find(
							(v) => v.type === 'authProvider'
						)?.name;
						licenseRequiredProvider = violationId
							? $adminConfigStore.authProviders.find((p) => p.id === violationId)
							: undefined;
					}}
				>
					<KeyRound class="size-4" />
					Register Your Email
				</button>
			{/if}
			<a href="mailto:info@obot.ai" class="btn btn-secondary">
				<Mail class="size-4" />
				Contact Support
			</a>
		</div>
		{#if !warnUserLimit && violations.authProvider}
			<div class="divider">OR</div>
			<div class="flex flex-col gap-4">
				{#each (version.current.licenseEntitlementViolations ?? []).filter((v) => v.type === 'authProvider') as violation (violation.name)}
					{@const provider = $adminConfigStore.authProviders.find((p) => p.id === violation.name)}
					{#if provider}
						<div class="flex justify-between gap-4">
							<div class="dark:bg-base-400 p-1 rounded-md shrink-0">
								{#if darkMode.isDark}
									{@const url = provider.iconDark || provider.icon}
									<img src={url} alt={provider.name} class="size-10 rounded-md p-1" />
								{:else}
									<img
										src={provider.icon}
										alt={provider.name}
										class="size-10 rounded-md p-1 dark:bg-base-400"
									/>
								{/if}
							</div>
							<div class="flex grow flex-col gap-0.5">
								<p class="font-semibold">Deconfigure {provider.name}</p>
								<p class="text-xs text-muted-content">
									Users logged in via {provider.name} will need to sign in via a different accessible
									provider.
								</p>
							</div>
						</div>
					{/if}
				{/each}
				{#if error}
					<div
						role="alert"
						class="alert alert-error alert-soft"
						in:slide={{ duration: 150, axis: 'y' }}
					>
						{error}
					</div>
				{/if}
				<button
					class="btn btn-error btn-soft mt-2"
					onclick={() => {
						licenseViolationDialog?.close();
						providersToDeconfigure = (version.current.licenseEntitlementViolations || [])
							.filter((violation) => violation.type === 'authProvider')
							.map((violation) =>
								$adminConfigStore.authProviders.find((p) => p.id === violation.name)
							)
							.filter((p): p is AuthProvider => p !== undefined);
						confirmDowngradeDialog?.open();
					}}
				>
					Downgrade
				</button>
			</div>
		{/if}
	</div>
</ResponsiveDialog>

<ProviderDeconfigureConfirm
	bind:this={confirmDowngradeDialog}
	onConfirm={handleDowngrade}
	onCancel={() => {
		confirmDowngradeDialog?.close();
		licenseViolationDialog?.open();
	}}
	loading={downgrading}
	providers={providersToDeconfigure}
	title="Confirm Downgrade"
	confirmButtonText="Downgrade"
/>

<LicenseProviderDialog
	bind:provider={licenseRequiredProvider}
	licenseKey={license.current.licenseKey}
	endpoint={AdminService.createCommunityLicense}
	onSubmit={() => reloadPage()}
	allowSignup
	signUpMessage="Get permanent, free access to additional authentication providers, including Entra, Okta, JumpCloud, and Auth0, with a one-time registration up to 100 users."
/>
