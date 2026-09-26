<script lang="ts">
	import LLMGatewayCodeBlock from '$lib/components/llm-gateway/LLMGatewayCodeBlock.svelte';
	import type { Model } from '$lib/services';
	import { twMerge } from 'tailwind-merge';

	interface Props {
		obotURL: string;
		models?: Model[];
		class?: string;
	}

	let { obotURL, models = [], class: klass = '' }: Props = $props();

	type Tab = 'claude-code' | 'codex' | 'openai-sdk' | 'anthropic-sdk' | 'curl';
	let tab = $state<Tab>('claude-code');

	let baseURL = $derived(`${obotURL}/api/llm-proxy/bifrost`);
	let token = $derived(`$(obot login --url ${obotURL} --scope llm --print-token)`);
	let anthropicModel = $derived(
		models.find((m) => m.dialect === 'AnthropicMessages')?.targetModel ??
			'anthropic/claude-sonnet-4-5'
	);
	let openaiModel = $derived(
		models.find((m) => m.dialect !== 'AnthropicMessages')?.targetModel ??
			models[0]?.targetModel ??
			'openai/gpt-4.1'
	);

	const tabs: { id: Tab; label: string }[] = [
		{ id: 'claude-code', label: 'Claude Code' },
		{ id: 'codex', label: 'Codex CLI' },
		{ id: 'openai-sdk', label: 'OpenAI SDK' },
		{ id: 'anthropic-sdk', label: 'Anthropic SDK' },
		{ id: 'curl', label: 'curl' }
	];

	let blocks = $derived.by(() => {
		switch (tab) {
			case 'claude-code':
				return [
					{
						language: 'bash' as const,
						code: [
							`export ANTHROPIC_BASE_URL="${baseURL}"`,
							`export ANTHROPIC_AUTH_TOKEN="${token}"`,
							`# Any gateway model works, including non-Anthropic ones:`,
							`export ANTHROPIC_MODEL="${anthropicModel}"`,
							'claude'
						].join('\n')
					}
				];
			case 'codex':
				return [
					{
						title: '~/.codex/config.toml',
						language: 'toml' as const,
						code: [
							`model = "${openaiModel}"`,
							`model_provider = "obot"`,
							'',
							'[model_providers.obot]',
							'name = "Obot LLM Gateway"',
							`base_url = "${baseURL}/v1"`,
							'env_key = "OBOT_API_KEY"',
							'wire_api = "responses"'
						].join('\n')
					},
					{
						language: 'bash' as const,
						code: `export OBOT_API_KEY="${token}"\ncodex`
					}
				];
			case 'openai-sdk':
				return [
					{
						language: 'bash' as const,
						code: [
							`export OPENAI_BASE_URL="${baseURL}/v1"`,
							`export OPENAI_API_KEY="${token}"`,
							'',
							'python - <<PY',
							'from openai import OpenAI',
							'client = OpenAI()',
							`r = client.chat.completions.create(model="${openaiModel}", messages=[{"role": "user", "content": "hello"}])`,
							'print(r.choices[0].message.content)',
							'PY'
						].join('\n')
					}
				];
			case 'anthropic-sdk':
				return [
					{
						language: 'bash' as const,
						code: [
							`export ANTHROPIC_BASE_URL="${baseURL}"`,
							`export ANTHROPIC_AUTH_TOKEN="${token}"`,
							'',
							'python - <<PY',
							'import anthropic',
							'client = anthropic.Anthropic()',
							`m = client.messages.create(model="${anthropicModel}", max_tokens=512, messages=[{"role": "user", "content": "hello"}])`,
							'print(m.content[0].text)',
							'PY'
						].join('\n')
					}
				];
			case 'curl':
				return [
					{
						title: 'OpenAI Chat Completions (any model)',
						language: 'bash' as const,
						code: [
							`curl ${baseURL}/v1/chat/completions \\`,
							`  -H "Authorization: Bearer ${token}" \\`,
							'  -H "content-type: application/json" \\',
							`  -d '{"model": "${openaiModel}", "messages": [{"role": "user", "content": "hello"}], "fallbacks": ["${anthropicModel}"]}'`
						].join('\n')
					},
					{
						title: 'Anthropic Messages (any model)',
						language: 'bash' as const,
						code: [
							`curl ${baseURL}/v1/messages \\`,
							`  -H "Authorization: Bearer ${token}" \\`,
							'  -H "anthropic-version: 2023-06-01" \\',
							'  -H "content-type: application/json" \\',
							`  -d '{"model": "${anthropicModel}", "max_tokens": 512, "messages": [{"role": "user", "content": "hello"}]}'`
						].join('\n')
					}
				];
		}
	});
</script>

<div class={twMerge('flex flex-col gap-3', klass)}>
	<div class="flex flex-wrap gap-1" role="tablist">
		{#each tabs as t (t.id)}
			<button
				role="tab"
				aria-selected={tab === t.id}
				class={twMerge(
					'rounded-md px-3 py-1 text-xs font-medium transition-colors',
					tab === t.id
						? 'bg-primary text-primary-content'
						: 'bg-base-200 dark:bg-base-300 hover:bg-base-300 dark:hover:bg-base-400'
				)}
				onclick={() => (tab = t.id)}
			>
				{t.label}
			</button>
		{/each}
	</div>
	{#each blocks as block, i (tab + i)}
		<LLMGatewayCodeBlock {block} />
	{/each}
	<p class="text-muted-content text-xs">
		Every model is available in the OpenAI Chat Completions, OpenAI Responses and Anthropic Messages
		formats. Models are named <code>provider/model</code>; bare names like
		<code>claude-sonnet-4-5</code> also work. Add <code>"fallbacks"</code> to a request to fail over to
		other models you can use.
	</p>
</div>
