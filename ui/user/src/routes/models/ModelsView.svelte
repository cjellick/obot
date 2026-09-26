<script lang="ts">
	import { afterNavigate } from '$app/navigation';
	import GatewayConnect from '$lib/components/llm-gateway/GatewayConnect.svelte';
	import LLMGatewayProviderSection from '$lib/components/llm-gateway/LLMGatewayProviderSection.svelte';
	import { CommonModelProviderIds, PAGE_TRANSITION_DURATION } from '$lib/constants';
	import type { Model } from '$lib/services';
	import {
		PROVIDER_CONNECTIONS,
		type ProviderShortKey,
		type RenderContext
	} from '$lib/services/llm-gateway/types';
	import accessibleModels from '$lib/stores/accessibleModels.svelte';
	import { onMount } from 'svelte';
	import { fade } from 'svelte/transition';

	let obotURL = $state('');
	let models = $derived(accessibleModels.current);

	onMount(() => {
		obotURL = window.location.origin;
	});

	afterNavigate(({ type }) => {
		if (type !== 'enter') {
			void accessibleModels.refresh();
		}
	});

	let gatewayModels = $derived(
		models.filter((m) => m.modelProvider === CommonModelProviderIds.BIFROST)
	);
	let openaiModels = $derived(
		models.filter((m) => m.modelProvider === CommonModelProviderIds.OPENAI)
	);
	let anthropicModels = $derived(
		models.filter((m) => m.modelProvider === CommonModelProviderIds.ANTHROPIC)
	);
	let genericResponsesModels = $derived(
		models.filter((m) => m.modelProvider === CommonModelProviderIds.GENERIC_RESPONSES)
	);
	let genericResponsesDisplayModels = $derived(toCallableModelNames(genericResponsesModels));
	let bedrockModels = $derived(
		models.filter((m) => m.modelProvider === CommonModelProviderIds.AMAZON_BEDROCK)
	);
	let bedrockAPIKeyModels = $derived(
		models.filter((m) => m.modelProvider === CommonModelProviderIds.AMAZON_BEDROCK_API_KEY)
	);
	let bedrockAnthropicModels = $derived(bedrockModels.filter(isBedrockAnthropicModel));
	let bedrockOpenAIModels = $derived(bedrockModels.filter(isBedrockOpenAICompatibleModel));
	let bedrockAPIKeyAnthropicModels = $derived(bedrockAPIKeyModels.filter(isBedrockAnthropicModel));
	let bedrockAPIKeyOpenAIModels = $derived(
		bedrockAPIKeyModels.filter(isBedrockOpenAICompatibleModel)
	);
	let bedrockAnthropicDisplayModels = $derived(toCallableModelNames(bedrockAnthropicModels));
	let bedrockOpenAIDisplayModels = $derived(toCallableModelNames(bedrockOpenAIModels));
	let bedrockAPIKeyAnthropicDisplayModels = $derived(
		toCallableModelNames(bedrockAPIKeyAnthropicModels)
	);
	let bedrockAPIKeyOpenAIDisplayModels = $derived(toCallableModelNames(bedrockAPIKeyOpenAIModels));
	let azureModels = $derived(
		models.filter((m) => m.modelProvider === CommonModelProviderIds.AZURE)
	);
	let azureEntraModels = $derived(
		models.filter((m) => m.modelProvider === CommonModelProviderIds.AZURE_ENTRA)
	);
	let azureAnthropicModels = $derived(azureModels.filter(isAnthropicDialect));
	let azureOpenAIModels = $derived(azureModels.filter(isOpenAIDialect));
	let azureEntraAnthropicModels = $derived(azureEntraModels.filter(isAnthropicDialect));
	let azureEntraOpenAIModels = $derived(azureEntraModels.filter(isOpenAIDialect));
	let azureAnthropicDisplayModels = $derived(toCallableModelNames(azureAnthropicModels));
	let azureOpenAIDisplayModels = $derived(toCallableModelNames(azureOpenAIModels));
	let azureEntraAnthropicDisplayModels = $derived(toCallableModelNames(azureEntraAnthropicModels));
	let azureEntraOpenAIDisplayModels = $derived(toCallableModelNames(azureEntraOpenAIModels));

	function modelID(model: Model) {
		return model.targetModel || model.name;
	}

	function isBedrockAnthropicModel(model: Model) {
		return modelID(model).startsWith('anthropic.');
	}

	function isBedrockOpenAICompatibleModel(model: Model) {
		const id = modelID(model);
		return id.startsWith('openai.') || id.startsWith('google.');
	}

	function isAnthropicDialect(model: Model) {
		return model.dialect === 'AnthropicMessages';
	}

	function isOpenAIDialect(model: Model) {
		return model.dialect === 'OpenAIResponses';
	}

	function toCallableModelNames(models: Model[]): Model[] {
		return models.map((model) => ({
			...model,
			name: modelID(model)
		}));
	}

	function buildCtx(shortKey: ProviderShortKey, models: Model[]): RenderContext {
		const provider = PROVIDER_CONNECTIONS[shortKey];
		return {
			provider,
			obotURL,
			baseURL: `${obotURL}/api/llm-proxy/${provider.routePath}`,
			exampleModel: models[0]?.name
		};
	}

	let openaiCtx = $derived(buildCtx('openai', openaiModels));
	let anthropicCtx = $derived(buildCtx('anthropic', anthropicModels));
	let genericResponsesCtx = $derived(buildCtx('generic-responses', genericResponsesDisplayModels));
	let bedrockAnthropicCtx = $derived(
		buildCtx('aws-bedrock-anthropic', bedrockAnthropicDisplayModels)
	);
	let bedrockOpenAICtx = $derived(buildCtx('aws-bedrock-openai', bedrockOpenAIDisplayModels));
	let bedrockAPIKeyAnthropicCtx = $derived(
		buildCtx('aws-bedrock-api-key-anthropic', bedrockAPIKeyAnthropicDisplayModels)
	);
	let bedrockAPIKeyOpenAICtx = $derived(
		buildCtx('aws-bedrock-api-key-openai', bedrockAPIKeyOpenAIDisplayModels)
	);
	let azureAnthropicCtx = $derived(buildCtx('azure-anthropic', azureAnthropicDisplayModels));
	let azureOpenAICtx = $derived(buildCtx('azure-openai', azureOpenAIDisplayModels));
	let azureEntraAnthropicCtx = $derived(
		buildCtx('azure-entra-anthropic', azureEntraAnthropicDisplayModels)
	);
	let azureEntraOpenAICtx = $derived(buildCtx('azure-entra-openai', azureEntraOpenAIDisplayModels));

	let ready = $derived(obotURL !== '');

	const duration = PAGE_TRANSITION_DURATION;
</script>

<div class="flex h-full w-full flex-col gap-6" in:fade={{ duration }}>
	<p class="text-muted-content max-w-3xl text-sm">
		Use the Obot LLM Gateway to call OpenAI, Anthropic, Generic Responses, Amazon Bedrock, and Azure
		models with your Obot credentials. Configure your client below, then pick from the models you
		have access to.
	</p>

	{#if ready}
		<div class="flex flex-col gap-4">
			{#if gatewayModels.length > 0}
				<div class="paper flex flex-col gap-4 p-4">
					<div class="flex items-center gap-3">
						<img src="/admin/assets/bifrost_icon.svg" alt="" class="size-8 rounded-md" />
						<div class="flex flex-col">
							<h3 class="text-base font-semibold">LLM Gateway</h3>
							<p class="text-muted-content text-xs">
								{gatewayModels.length} models across every configured provider, in any API format.
							</p>
						</div>
					</div>
					<GatewayConnect {obotURL} models={gatewayModels} />
					<details class="text-sm">
						<summary class="cursor-pointer font-medium">Available models</summary>
						<div class="mt-2 flex flex-wrap gap-1.5">
							{#each gatewayModels as m (m.id)}
								<span class="bg-base-200 dark:bg-base-300 rounded px-2 py-0.5 font-mono text-xs"
									>{m.targetModel}</span
								>
							{/each}
						</div>
					</details>
				</div>
			{/if}
			{#if anthropicModels.length > 0}
				<LLMGatewayProviderSection ctx={anthropicCtx} models={anthropicModels} />
			{/if}
			{#if openaiModels.length > 0}
				<LLMGatewayProviderSection ctx={openaiCtx} models={openaiModels} />
			{/if}
			{#if genericResponsesModels.length > 0}
				<LLMGatewayProviderSection
					ctx={genericResponsesCtx}
					models={genericResponsesDisplayModels}
				/>
			{/if}
			{#if bedrockAnthropicModels.length > 0}
				<LLMGatewayProviderSection
					ctx={bedrockAnthropicCtx}
					models={bedrockAnthropicDisplayModels}
				/>
			{/if}
			{#if bedrockOpenAIModels.length > 0}
				<LLMGatewayProviderSection ctx={bedrockOpenAICtx} models={bedrockOpenAIDisplayModels} />
			{/if}
			{#if bedrockAPIKeyAnthropicModels.length > 0}
				<LLMGatewayProviderSection
					ctx={bedrockAPIKeyAnthropicCtx}
					models={bedrockAPIKeyAnthropicDisplayModels}
				/>
			{/if}
			{#if bedrockAPIKeyOpenAIModels.length > 0}
				<LLMGatewayProviderSection
					ctx={bedrockAPIKeyOpenAICtx}
					models={bedrockAPIKeyOpenAIDisplayModels}
				/>
			{/if}
			{#if azureAnthropicModels.length > 0}
				<LLMGatewayProviderSection ctx={azureAnthropicCtx} models={azureAnthropicDisplayModels} />
			{/if}
			{#if azureOpenAIModels.length > 0}
				<LLMGatewayProviderSection ctx={azureOpenAICtx} models={azureOpenAIDisplayModels} />
			{/if}
			{#if azureEntraAnthropicModels.length > 0}
				<LLMGatewayProviderSection
					ctx={azureEntraAnthropicCtx}
					models={azureEntraAnthropicDisplayModels}
				/>
			{/if}
			{#if azureEntraOpenAIModels.length > 0}
				<LLMGatewayProviderSection
					ctx={azureEntraOpenAICtx}
					models={azureEntraOpenAIDisplayModels}
				/>
			{/if}
		</div>
	{/if}
</div>
