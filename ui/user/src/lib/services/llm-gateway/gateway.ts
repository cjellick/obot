// Client for Obot's LLM gateway API. Obot embeds Bifrost (https://github.com/maximhq/bifrost)
// and exposes its management API under /api/llm-gateway/admin, alongside Obot-specific
// endpoints for status, model sync, budgets and guardrails.
import { doDelete, doGet, doPost, doPut, type Fetcher } from '../http';

type Opts = { fetch?: Fetcher; signal?: AbortSignal; dontLogErrors?: boolean };

const B = '/llm-gateway/admin';

// ---------- Obot endpoints ----------

export interface GatewayStatus {
	enabled: boolean;
	ready: boolean;
	error?: string;
	hasProviders: boolean;
	modelProviderName: string;
	proxyPath: string;
}

export async function getStatus(opts?: Opts): Promise<GatewayStatus> {
	return (await doGet('/llm-gateway/status', opts)) as GatewayStatus;
}

export async function syncModels(opts?: Opts): Promise<void> {
	await doPost('/llm-gateway/sync-models', {}, opts);
}

// ---------- Budgets (enforced by Obot) ----------

export type BudgetMetric = 'spend' | 'tokens' | 'input_tokens' | 'output_tokens' | 'requests';

export interface BudgetLimit {
	metric: BudgetMetric;
	amount: number;
	window: string;
	calendar?: boolean;
}

export type BudgetSubjectType = 'user' | 'group' | 'everyone';

export interface BudgetSubject {
	type: BudgetSubjectType;
	id?: string;
}

export interface Budget {
	id?: string;
	name: string;
	description?: string;
	enabled: boolean;
	perUser: boolean;
	exemptAdmins: boolean;
	// Users' personal daily token limits replace this per-user budget's token limits.
	userTokenLimits?: boolean;
	limits: BudgetLimit[];
	models?: string[];
	subjects: BudgetSubject[];
	created?: string;
	updated?: string;
}

export interface BudgetUsageEntry {
	userID?: string;
	used: number[];
}

export async function listBudgets(opts?: Opts): Promise<Budget[]> {
	const resp = (await doGet('/llm-gateway/budgets', opts)) as { items: Budget[] };
	return resp.items ?? [];
}

export async function getBudget(id: string, opts?: Opts): Promise<Budget> {
	return (await doGet(`/llm-gateway/budgets/${encodeURIComponent(id)}`, opts)) as Budget;
}

export async function createBudget(budget: Budget, opts?: Opts): Promise<Budget> {
	return (await doPost('/llm-gateway/budgets', budget, opts)) as Budget;
}

export async function updateBudget(id: string, budget: Budget, opts?: Opts): Promise<Budget> {
	return (await doPut(`/llm-gateway/budgets/${encodeURIComponent(id)}`, budget, opts)) as Budget;
}

export async function deleteBudget(id: string, opts?: Opts): Promise<void> {
	await doDelete(`/llm-gateway/budgets/${encodeURIComponent(id)}`, opts);
}

export async function getBudgetUsage(id: string, opts?: Opts): Promise<BudgetUsageEntry[]> {
	const resp = (await doGet(`/llm-gateway/budgets/${encodeURIComponent(id)}/usage`, opts)) as {
		items: BudgetUsageEntry[];
	};
	return resp.items ?? [];
}

export const METRIC_LABELS: Record<BudgetMetric, string> = {
	spend: 'Spend (USD)',
	tokens: 'Total tokens',
	input_tokens: 'Input tokens',
	output_tokens: 'Output tokens',
	requests: 'Requests'
};

export function formatMetric(metric: BudgetMetric, value: number): string {
	return metric === 'spend' ? formatCost(value) : formatNumber(value);
}

export function windowLabel(limit: Pick<BudgetLimit, 'window' | 'calendar'>): string {
	const m = /^(\d+)([mhdwM])$/.exec(limit.window);
	if (!m) return limit.window;
	const units: Record<string, string> = { m: 'minute', h: 'hour', d: 'day', w: 'week', M: 'month' };
	const n = Number(m[1]);
	const unit = units[m[2]];
	if (limit.calendar) return `per calendar ${unit}`;
	return n === 1 ? `per rolling ${unit}` : `per rolling ${n} ${unit}s`;
}

// ---------- Providers & keys ----------

export interface NetworkConfig {
	base_url?: string;
	extra_headers?: Record<string, string>;
	default_request_timeout_in_seconds?: number;
	max_retries?: number;
	retry_backoff_initial?: number;
	retry_backoff_max?: number;
	stream_idle_timeout_in_seconds?: number;
	allow_private_network?: boolean;
	insecure_skip_verify?: boolean;
}

export interface ConcurrencyAndBufferSize {
	concurrency: number;
	buffer_size: number;
}

export interface CustomProviderConfig {
	is_key_less?: boolean;
	base_provider_type: string;
}

export interface Provider {
	name: string;
	network_config: NetworkConfig;
	concurrency_and_buffer_size: ConcurrencyAndBufferSize;
	custom_provider_config?: CustomProviderConfig;
	send_back_raw_request?: boolean;
	send_back_raw_response?: boolean;
	provider_status?: string;
	status?: string;
	description?: string;
}

export interface SecretValue {
	value: string;
	type?: string;
	env_var?: string;
}

export interface ProviderKey {
	id?: string;
	name: string;
	value: SecretValue | string;
	models: string[];
	blacklisted_models?: string[];
	weight: number;
	enabled?: boolean;
	status?: string;
	description?: string;
	azure_key_config?: Record<string, unknown>;
	vertex_key_config?: Record<string, unknown>;
	bedrock_key_config?: Record<string, unknown>;
	vllm_key_config?: Record<string, unknown>;
	ollama_key_config?: Record<string, unknown>;
	sgl_key_config?: Record<string, unknown>;
	databricks_key_config?: Record<string, unknown>;
	use_for_batch_api?: boolean;
}

export async function listProviders(opts?: Opts): Promise<Provider[]> {
	const resp = (await doGet(`${B}/providers`, opts)) as { providers: Provider[] };
	return resp.providers ?? [];
}

export async function getProvider(name: string, opts?: Opts): Promise<Provider> {
	return (await doGet(`${B}/providers/${encodeURIComponent(name)}`, opts)) as Provider;
}

export async function createProvider(
	body: Partial<Provider> & { provider: string },
	opts?: Opts
): Promise<Provider> {
	return (await doPost(`${B}/providers`, body, opts)) as Provider;
}

export async function updateProvider(
	name: string,
	body: Partial<Provider>,
	opts?: Opts
): Promise<Provider> {
	return (await doPut(`${B}/providers/${encodeURIComponent(name)}`, body, opts)) as Provider;
}

export async function deleteProvider(name: string, opts?: Opts): Promise<void> {
	await doDelete(`${B}/providers/${encodeURIComponent(name)}`, opts);
}

export async function listProviderKeys(name: string, opts?: Opts): Promise<ProviderKey[]> {
	const resp = (await doGet(`${B}/providers/${encodeURIComponent(name)}/keys`, opts)) as {
		keys: ProviderKey[];
	};
	return resp.keys ?? [];
}

export async function createProviderKey(
	provider: string,
	key: ProviderKey,
	opts?: Opts
): Promise<ProviderKey> {
	return (await doPost(
		`${B}/providers/${encodeURIComponent(provider)}/keys`,
		key,
		opts
	)) as ProviderKey;
}

export async function updateProviderKey(
	provider: string,
	id: string,
	key: ProviderKey,
	opts?: Opts
): Promise<ProviderKey> {
	return (await doPut(
		`${B}/providers/${encodeURIComponent(provider)}/keys/${encodeURIComponent(id)}`,
		key,
		opts
	)) as ProviderKey;
}

export async function deleteProviderKey(provider: string, id: string, opts?: Opts): Promise<void> {
	await doDelete(
		`${B}/providers/${encodeURIComponent(provider)}/keys/${encodeURIComponent(id)}`,
		opts
	);
}

export async function refreshProviderModels(provider: string, opts?: Opts): Promise<void> {
	await doPost(`${B}/providers/${encodeURIComponent(provider)}/refresh-models`, {}, opts);
}

export interface GatewayModel {
	name: string;
	provider: string;
	is_deprecated?: boolean;
}

export async function listGatewayModels(
	params: { provider?: string; query?: string; limit?: number } = {},
	opts?: Opts
): Promise<GatewayModel[]> {
	const q = new URLSearchParams();
	if (params.provider) q.set('provider', params.provider);
	if (params.query) q.set('query', params.query);
	q.set('limit', String(params.limit ?? 500));
	const resp = (await doGet(`${B}/models?${q}`, opts)) as { models: GatewayModel[] };
	return resp.models ?? [];
}

// ---------- Routing rules ----------

export interface RoutingTarget {
	provider?: string | null;
	model?: string | null;
	key_id?: string | null;
	weight: number;
}

export interface RoutingFallback {
	provider: string;
	model: string;
}

export interface RoutingRule {
	id?: string;
	name: string;
	description?: string;
	enabled?: boolean;
	cel_expression: string;
	targets: RoutingTarget[];
	// Bifrost returns unpinned fallbacks as "provider/model" strings.
	fallbacks?: (RoutingFallback | string)[];
	scope?: string;
	scope_id?: string | null;
	priority?: number;
	chain_rule?: boolean;
}

export async function listRoutingRules(opts?: Opts): Promise<RoutingRule[]> {
	const resp = (await doGet(`${B}/governance/routing-rules?limit=500`, opts)) as {
		rules: RoutingRule[] | null;
	};
	return resp.rules ?? [];
}

export async function createRoutingRule(rule: RoutingRule, opts?: Opts): Promise<RoutingRule> {
	const resp = (await doPost(`${B}/governance/routing-rules`, rule, opts)) as {
		rule: RoutingRule;
	};
	return resp.rule;
}

export async function updateRoutingRule(
	id: string,
	rule: Partial<RoutingRule>,
	opts?: Opts
): Promise<RoutingRule> {
	const resp = (await doPut(
		`${B}/governance/routing-rules/${encodeURIComponent(id)}`,
		rule,
		opts
	)) as { rule: RoutingRule };
	return resp.rule;
}

export async function deleteRoutingRule(id: string, opts?: Opts): Promise<void> {
	await doDelete(`${B}/governance/routing-rules/${encodeURIComponent(id)}`, opts);
}

export function parseFallback(f: RoutingFallback | string): RoutingFallback {
	if (typeof f !== 'string') return f;
	const i = f.indexOf('/');
	return i < 0 ? { provider: '', model: f } : { provider: f.slice(0, i), model: f.slice(i + 1) };
}

// ---------- Logs & analytics ----------

export interface TokenUsage {
	prompt_tokens: number;
	completion_tokens: number;
	total_tokens: number;
}

export interface GatewayLog {
	id: string;
	timestamp: string;
	object: string;
	provider: string;
	model: string;
	status: string;
	stream: boolean;
	latency?: number;
	cost?: number;
	number_of_retries?: number;
	fallback_index?: number;
	selected_key_name?: string;
	routing_rule_name?: string | null;
	token_usage?: TokenUsage;
	metadata?: Record<string, string>;
	content_summary?: string;
	error_details?: { error?: { message?: string } };
	input_history?: unknown[];
	responses_input_history?: unknown[];
	output_message?: unknown;
	responses_output?: unknown;
	params?: unknown;
	raw_request?: string;
	raw_response?: string;
}

export interface LogStats {
	total_requests: number;
	success_rate: number;
	average_latency: number;
	total_tokens: number;
	prompt_tokens: number;
	completion_tokens: number;
	total_cost: number;
}

export interface LogFilters {
	providers?: string[];
	models?: string[];
	status?: string[];
	objects?: string[];
	start_time?: string;
	end_time?: string;
	content_search?: string;
	metadata?: Record<string, string>;
}

function filtersToQuery(filters: LogFilters): URLSearchParams {
	const q = new URLSearchParams();
	if (filters.providers?.length) q.set('providers', filters.providers.join(','));
	if (filters.models?.length) q.set('models', filters.models.join(','));
	if (filters.status?.length) q.set('status', filters.status.join(','));
	if (filters.objects?.length) q.set('objects', filters.objects.join(','));
	if (filters.start_time) q.set('start_time', filters.start_time);
	if (filters.end_time) q.set('end_time', filters.end_time);
	if (filters.content_search) q.set('content_search', filters.content_search);
	for (const [k, v] of Object.entries(filters.metadata ?? {})) {
		q.set(`metadata_${k}`, v);
	}
	return q;
}

// Obot lists models on Bifrost regularly; those calls are noise in the request log.
export const INFERENCE_OBJECTS = [
	'chat_completion',
	'chat_completion_stream',
	'text_completion',
	'text_completion_stream',
	'responses',
	'responses_stream',
	'embedding',
	'speech',
	'speech_stream',
	'transcription',
	'transcription_stream',
	'image_generation',
	'image_generation_stream',
	'rerank',
	'count_tokens'
];

export async function listLogs(
	filters: LogFilters,
	page: { limit: number; offset: number },
	opts?: Opts
): Promise<{ logs: GatewayLog[]; total: number; stats: LogStats }> {
	const q = filtersToQuery(filters);
	q.set('limit', String(page.limit));
	q.set('offset', String(page.offset));
	q.set('sort_by', 'timestamp');
	q.set('order', 'desc');
	const resp = (await doGet(`${B}/logs?${q}`, opts)) as {
		logs: GatewayLog[] | null;
		pagination: { total_count: number };
		stats: LogStats;
	};
	return { logs: resp.logs ?? [], total: resp.pagination?.total_count ?? 0, stats: resp.stats };
}

export async function getLog(id: string, opts?: Opts): Promise<GatewayLog> {
	return (await doGet(`${B}/logs/${encodeURIComponent(id)}`, opts)) as GatewayLog;
}

export interface LogFilterData {
	models: string[];
	selected_keys?: { id: string; name: string }[];
	metadata_keys?: Record<string, string[]>;
}

export async function getLogFilterData(opts?: Opts): Promise<LogFilterData> {
	return (await doGet(`${B}/logs/filterdata`, opts)) as LogFilterData;
}

export interface HistogramBucket {
	timestamp: string;
	count?: number;
	success?: number;
	error?: number;
	total_cost?: number;
	by_provider?: Record<string, number | TokenUsage>;
	by_model?: Record<string, number>;
	prompt_tokens?: number;
	completion_tokens?: number;
	total_tokens?: number;
	avg_latency?: number;
	p50_latency?: number;
	p95_latency?: number;
	p99_latency?: number;
}

export interface Histogram {
	buckets: HistogramBucket[];
	bucket_size_seconds: number;
	providers?: string[];
}

export async function getHistogram(
	kind:
		| 'histogram'
		| 'histogram/tokens'
		| 'histogram/cost'
		| 'histogram/latency'
		| 'histogram/cost/by-provider'
		| 'histogram/tokens/by-provider'
		| 'histogram/latency/by-provider',
	filters: LogFilters,
	opts?: Opts
): Promise<Histogram> {
	const q = filtersToQuery(filters);
	return (await doGet(`${B}/logs/${kind}?${q}`, opts)) as Histogram;
}

export async function getStats(filters: LogFilters, opts?: Opts): Promise<LogStats> {
	const q = filtersToQuery(filters);
	return (await doGet(`${B}/logs/stats?${q}`, opts)) as LogStats;
}

export interface ModelRanking {
	model: string;
	provider: string;
	total_requests: number;
	success_rate: number;
	total_tokens: number;
	total_cost: number;
	avg_latency: number;
}

export async function getRankings(filters: LogFilters, opts?: Opts): Promise<ModelRanking[]> {
	const q = filtersToQuery(filters);
	const resp = (await doGet(`${B}/logs/rankings?${q}`, opts)) as {
		rankings: ModelRanking[] | null;
	};
	return resp.rankings ?? [];
}

// ---------- Settings ----------

export interface ClientConfig {
	drop_excess_requests: boolean;
	enable_logging: boolean;
	disable_content_logging: boolean;
	log_retention_days: number;
	max_request_body_size_mb: number;
	prometheus_labels: string[] | null;
	[key: string]: unknown;
}

export async function getConfig(opts?: Opts): Promise<{ client_config: ClientConfig }> {
	return (await doGet(`${B}/config`, opts)) as { client_config: ClientConfig };
}

export async function updateConfig(
	body: { client_config: Partial<ClientConfig> },
	opts?: Opts
): Promise<void> {
	await doPut(`${B}/config`, body, opts);
}

// ---------- Helpers ----------

export function formatCost(value?: number): string {
	if (!value) return '$0.00';
	if (value < 0.01) return `$${value.toFixed(6)}`;
	return `$${value.toFixed(2)}`;
}

export function formatNumber(value?: number): string {
	if (value === undefined || value === null) return '0';
	return new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(
		value
	);
}

export function secretDisplay(value: SecretValue | string | undefined): string {
	if (!value) return '';
	if (typeof value === 'string') return value;
	return value.env_var ? `env.${value.env_var}` : value.value;
}
