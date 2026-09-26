// Catalog of the upstream providers the embedded Bifrost gateway supports, with the
// configuration each one needs. Used by the provider setup forms.

export interface KeyField {
	// Path of the value in the key payload, e.g. "value" or "azure_key_config.endpoint".
	path: string;
	label: string;
	placeholder?: string;
	description?: string;
	sensitive?: boolean;
	required?: boolean;
	multiline?: boolean;
}

export interface ProviderCatalogEntry {
	id: string;
	label: string;
	icon?: string;
	description: string;
	keyFields: KeyField[];
	// Whether a base URL is required (self-hosted providers).
	baseUrlRequired?: boolean;
	baseUrlPlaceholder?: string;
	// Providers that are used without any key.
	keyless?: boolean;
}

const apiKey = (placeholder = 'sk-...'): KeyField => ({
	path: 'value',
	label: 'API Key',
	placeholder,
	sensitive: true,
	required: true
});

export const PROVIDER_CATALOG: ProviderCatalogEntry[] = [
	{
		id: 'openai',
		label: 'OpenAI',
		icon: '/admin/assets/obot_openai_antenna_icon.png',
		description: 'GPT, o-series, embeddings, images and audio models.',
		keyFields: [apiKey()]
	},
	{
		id: 'anthropic',
		label: 'Anthropic',
		icon: '/admin/assets/anthropic_icon.svg',
		description: 'Claude models.',
		keyFields: [apiKey('sk-ant-...')]
	},
	{
		id: 'gemini',
		label: 'Google Gemini',
		icon: '/admin/assets/gemini_icon.svg',
		description: 'Gemini models through the Google AI Studio API.',
		keyFields: [apiKey('AIza...')]
	},
	{
		id: 'vertex',
		label: 'Google Vertex AI',
		icon: '/admin/assets/google_icon_small.png',
		description: 'Gemini, Claude and partner models on Google Cloud.',
		keyFields: [
			{ path: 'vertex_key_config.project_id', label: 'Project ID', required: true },
			{
				path: 'vertex_key_config.region',
				label: 'Region',
				placeholder: 'us-central1',
				required: true
			},
			{
				path: 'vertex_key_config.auth_credentials',
				label: 'Service Account JSON',
				sensitive: true,
				multiline: true,
				description: 'Leave empty to use the server’s default Google credentials.'
			}
		]
	},
	{
		id: 'bedrock',
		label: 'AWS Bedrock',
		icon: '/admin/assets/amazon-bedrock.png',
		description: 'Claude, Llama, Nova, Mistral and more on AWS.',
		keyFields: [
			{
				path: 'value',
				label: 'Bedrock API Key',
				sensitive: true,
				description: 'Use either a Bedrock API key or an access key pair.'
			},
			{ path: 'bedrock_key_config.access_key', label: 'Access Key ID' },
			{ path: 'bedrock_key_config.secret_key', label: 'Secret Access Key', sensitive: true },
			{ path: 'bedrock_key_config.session_token', label: 'Session Token', sensitive: true },
			{
				path: 'bedrock_key_config.region',
				label: 'Region',
				placeholder: 'us-east-1',
				required: true
			},
			{
				path: 'bedrock_key_config.role_arn',
				label: 'Role ARN',
				description: 'Optional IAM role to assume.'
			}
		]
	},
	{
		id: 'azure',
		label: 'Azure OpenAI / AI Foundry',
		icon: '/admin/assets/azure_openai_icon.svg',
		description: 'OpenAI and partner models deployed on Azure.',
		keyFields: [
			{
				path: 'azure_key_config.endpoint',
				label: 'Endpoint',
				placeholder: 'https://my-resource.openai.azure.com',
				required: true
			},
			{
				path: 'value',
				label: 'API Key',
				sensitive: true,
				description: 'Leave empty to authenticate with Entra ID below.'
			},
			{ path: 'azure_key_config.client_id', label: 'Entra Client ID' },
			{ path: 'azure_key_config.client_secret', label: 'Entra Client Secret', sensitive: true },
			{ path: 'azure_key_config.tenant_id', label: 'Entra Tenant ID' }
		]
	},
	{
		id: 'mistral',
		label: 'Mistral AI',
		description: 'Mistral and Codestral models.',
		keyFields: [apiKey('')]
	},
	{
		id: 'groq',
		label: 'Groq',
		icon: '/admin/assets/groq_logo.svg',
		description: 'Low-latency inference for open models.',
		keyFields: [apiKey('gsk_...')]
	},
	{
		id: 'xai',
		label: 'xAI',
		icon: '/admin/assets/xai-logo.svg',
		description: 'Grok models.',
		keyFields: [apiKey('xai-...')]
	},
	{
		id: 'deepseek',
		label: 'DeepSeek',
		icon: '/admin/assets/deepseek-logo.svg',
		description: 'DeepSeek chat and reasoning models.',
		keyFields: [apiKey()]
	},
	{
		id: 'openrouter',
		label: 'OpenRouter',
		description: 'Hundreds of models behind one API.',
		keyFields: [apiKey('sk-or-...')]
	},
	{
		id: 'cohere',
		label: 'Cohere',
		description: 'Command, Embed and Rerank models.',
		keyFields: [apiKey('')]
	},
	{
		id: 'perplexity',
		label: 'Perplexity',
		description: 'Sonar search-augmented models.',
		keyFields: [apiKey('pplx-...')]
	},
	{
		id: 'cerebras',
		label: 'Cerebras',
		description: 'Fast inference for open models.',
		keyFields: [apiKey('csk-...')]
	},
	{
		id: 'fireworks',
		label: 'Fireworks AI',
		description: 'Open models on Fireworks.',
		keyFields: [apiKey('fw_...')]
	},
	{
		id: 'huggingface',
		label: 'Hugging Face',
		description: 'Inference Providers on Hugging Face.',
		keyFields: [apiKey('hf_...')]
	},
	{
		id: 'nebius',
		label: 'Nebius Token Factory',
		description: 'Open models on Nebius.',
		keyFields: [apiKey('')]
	},
	{
		id: 'parasail',
		label: 'Parasail',
		description: 'Open models on Parasail.',
		keyFields: [apiKey('')]
	},
	{
		id: 'databricks',
		label: 'Databricks',
		description: 'Foundation models served by Databricks.',
		keyFields: [
			{
				path: 'databricks_key_config.workspace_url',
				label: 'Workspace URL',
				placeholder: 'https://my-workspace.cloud.databricks.com',
				required: true
			},
			{ path: 'value', label: 'Personal Access Token', sensitive: true }
		]
	},
	{
		id: 'ollama',
		label: 'Ollama',
		description: 'Local models served by Ollama.',
		keyFields: [
			{
				path: 'ollama_key_config.url',
				label: 'Server URL',
				placeholder: 'http://ollama:11434',
				required: true
			}
		]
	},
	{
		id: 'vllm',
		label: 'vLLM',
		icon: '/admin/assets/vllm-logo.svg',
		description: 'Self-hosted models served by vLLM.',
		keyFields: [
			{
				path: 'vllm_key_config.url',
				label: 'Server URL',
				placeholder: 'http://vllm:8000',
				required: true
			},
			{
				path: 'vllm_key_config.model_name',
				label: 'Served Model Name',
				required: true
			},
			{ path: 'value', label: 'API Key', sensitive: true }
		]
	},
	{
		id: 'sgl',
		label: 'SGLang',
		description: 'Self-hosted models served by SGLang.',
		keyFields: [
			{ path: 'sgl_key_config.url', label: 'Server URL', placeholder: 'http://sglang:30000' }
		]
	},
	{
		id: 'replicate',
		label: 'Replicate',
		description: 'Models hosted on Replicate.',
		keyFields: [apiKey('r8_...')]
	},
	{
		id: 'elevenlabs',
		label: 'ElevenLabs',
		description: 'Speech synthesis and transcription.',
		keyFields: [apiKey('')]
	}
];

// Custom providers speak one of these APIs at their own base URL.
export const CUSTOM_PROVIDER_BASES = [
	{ id: 'openai', label: 'OpenAI-compatible' },
	{ id: 'anthropic', label: 'Anthropic-compatible' },
	{ id: 'gemini', label: 'Gemini-compatible' },
	{ id: 'cohere', label: 'Cohere-compatible' }
];

export function catalogEntry(id: string): ProviderCatalogEntry | undefined {
	return PROVIDER_CATALOG.find((p) => p.id === id);
}

export function providerLabel(id: string): string {
	return catalogEntry(id)?.label ?? id;
}

export function providerIcon(id: string): string | undefined {
	return catalogEntry(id)?.icon;
}

export function getPath(obj: Record<string, unknown>, path: string): unknown {
	return path.split('.').reduce<unknown>((acc, part) => {
		if (acc && typeof acc === 'object') return (acc as Record<string, unknown>)[part];
		return undefined;
	}, obj);
}

export function setPath(obj: Record<string, unknown>, path: string, value: unknown) {
	const parts = path.split('.');
	let cur = obj;
	for (const part of parts.slice(0, -1)) {
		if (!cur[part] || typeof cur[part] !== 'object') cur[part] = {};
		cur = cur[part] as Record<string, unknown>;
	}
	cur[parts[parts.length - 1]] = value;
}
