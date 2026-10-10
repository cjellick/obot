// Client for Obot's guardrails API. Guardrail rules decide which LLM gateway requests and MCP
// tool calls are checked, by which profiles, and whether findings block, redact or are logged.
// Profiles configure the checkers, from built-in regex and secrets detection to external
// services.
import { doDelete, doGet, doPost, doPut, type Fetcher } from '../http';

type Opts = { fetch?: Fetcher; signal?: AbortSignal; dontLogErrors?: boolean };

const B = '/llm-gateway/guardrails';

export type GuardrailTarget = 'llm' | 'mcp';
export type GuardrailStage = 'input' | 'output';
export type GuardrailApplyTo = GuardrailStage | 'both';
export type GuardrailAction = 'block' | 'redact' | 'log';
export type GuardrailInputScope = 'all' | 'latest';
export type GuardrailOutcome = 'blocked' | 'redacted' | 'logged' | 'error';

export type GuardrailFieldType =
	| 'text'
	| 'textarea'
	| 'password'
	| 'number'
	| 'bool'
	| 'select'
	| 'list';

export interface GuardrailField {
	name: string;
	label: string;
	description?: string;
	type: GuardrailFieldType;
	options?: string[];
	default?: unknown;
	required?: boolean;
	sensitive?: boolean;
}

export interface GuardrailProvider {
	type: string;
	label: string;
	description: string;
	category: string;
	fields: GuardrailField[];
	redacts: boolean;
}

export interface GuardrailSubject {
	type: 'user' | 'group' | 'everyone';
	id?: string;
}

export interface GuardrailRule {
	id?: string;
	name: string;
	description?: string;
	enabled: boolean;
	target: GuardrailTarget;
	applyTo: GuardrailApplyTo;
	action: GuardrailAction;
	condition?: string;
	samplingRate: number;
	inputScope?: GuardrailInputScope;
	failClosed: boolean;
	blockMessage?: string;
	profileIDs: string[];
	subjects: GuardrailSubject[];
	created?: string;
	updated?: string;
}

export interface GuardrailProfile {
	id?: string;
	name: string;
	description?: string;
	type: string;
	config: Record<string, unknown>;
	configuredSecrets?: string[];
	error?: string;
	created?: string;
	updated?: string;
}

export interface GuardrailFinding {
	category: string;
	message?: string;
	score?: number;
	count?: number;
}

export interface GuardrailTestResult {
	findings: GuardrailFinding[];
	redactedText?: string;
	canRedact: boolean;
	error?: string;
}

export interface GuardrailEvent {
	id: number;
	created: string;
	ruleID: string;
	ruleName: string;
	profileID?: string;
	profileName?: string;
	profileType?: string;
	target: GuardrailTarget;
	stage: GuardrailStage;
	outcome: GuardrailOutcome;
	userID?: string;
	model?: string;
	resource?: string;
	findings?: GuardrailFinding[] | null;
	error?: string;
}

export interface GuardrailEventFilters {
	limit?: number;
	offset?: number;
	ruleID?: string;
	outcome?: GuardrailOutcome | '';
	target?: GuardrailTarget | '';
	since?: string;
	until?: string;
}

export const TARGET_LABELS: Record<GuardrailTarget, string> = {
	llm: 'LLM',
	mcp: 'MCP'
};

export const APPLY_TO_LABELS: Record<GuardrailApplyTo, string> = {
	input: 'Input',
	output: 'Output',
	both: 'Input & output'
};

export const ACTION_LABELS: Record<GuardrailAction, string> = {
	block: 'Block',
	redact: 'Redact',
	log: 'Log only'
};

export const OUTCOME_LABELS: Record<GuardrailOutcome, string> = {
	blocked: 'Blocked',
	redacted: 'Redacted',
	logged: 'Logged',
	error: 'Error'
};

export async function listGuardrailProviders(opts?: Opts): Promise<GuardrailProvider[]> {
	const resp = (await doGet(`${B}/providers`, opts)) as { items: GuardrailProvider[] | null };
	return resp.items ?? [];
}

export async function listGuardrailRules(opts?: Opts): Promise<GuardrailRule[]> {
	const resp = (await doGet(`${B}/rules`, opts)) as { items: GuardrailRule[] | null };
	return resp.items ?? [];
}

export async function getGuardrailRule(id: string, opts?: Opts): Promise<GuardrailRule> {
	return (await doGet(`${B}/rules/${encodeURIComponent(id)}`, opts)) as GuardrailRule;
}

export async function createGuardrailRule(
	rule: GuardrailRule,
	opts?: Opts
): Promise<GuardrailRule> {
	return (await doPost(`${B}/rules`, rule, opts)) as GuardrailRule;
}

export async function updateGuardrailRule(
	id: string,
	rule: GuardrailRule,
	opts?: Opts
): Promise<GuardrailRule> {
	return (await doPut(`${B}/rules/${encodeURIComponent(id)}`, rule, opts)) as GuardrailRule;
}

export async function deleteGuardrailRule(id: string, opts?: Opts): Promise<void> {
	await doDelete(`${B}/rules/${encodeURIComponent(id)}`, opts);
}

export async function validateGuardrailCondition(
	target: GuardrailTarget,
	condition: string,
	opts?: Opts
): Promise<{ valid: boolean; error?: string }> {
	return (await doPost(`${B}/validate-condition`, { target, condition }, opts)) as {
		valid: boolean;
		error?: string;
	};
}

export async function listGuardrailProfiles(opts?: Opts): Promise<GuardrailProfile[]> {
	const resp = (await doGet(`${B}/profiles`, opts)) as { items: GuardrailProfile[] | null };
	return resp.items ?? [];
}

export async function getGuardrailProfile(id: string, opts?: Opts): Promise<GuardrailProfile> {
	return (await doGet(`${B}/profiles/${encodeURIComponent(id)}`, opts)) as GuardrailProfile;
}

export async function createGuardrailProfile(
	profile: GuardrailProfile,
	opts?: Opts
): Promise<GuardrailProfile> {
	return (await doPost(`${B}/profiles`, profile, opts)) as GuardrailProfile;
}

export async function updateGuardrailProfile(
	id: string,
	profile: GuardrailProfile,
	opts?: Opts
): Promise<GuardrailProfile> {
	return (await doPut(
		`${B}/profiles/${encodeURIComponent(id)}`,
		profile,
		opts
	)) as GuardrailProfile;
}

export async function deleteGuardrailProfile(id: string, opts?: Opts): Promise<void> {
	await doDelete(`${B}/profiles/${encodeURIComponent(id)}`, opts);
}

export async function testGuardrailProfile(
	id: string,
	body: { target: GuardrailTarget; stage: GuardrailStage; text: string },
	opts?: Opts
): Promise<GuardrailTestResult> {
	return (await doPost(
		`${B}/profiles/${encodeURIComponent(id)}/test`,
		body,
		opts
	)) as GuardrailTestResult;
}

export async function listGuardrailEvents(
	filters: GuardrailEventFilters = {},
	opts?: Opts
): Promise<{ items: GuardrailEvent[]; total: number }> {
	const params = new URLSearchParams();
	for (const [key, value] of Object.entries(filters)) {
		if (value !== undefined && value !== '') params.set(key, String(value));
	}
	const query = params.toString();
	const resp = (await doGet(`${B}/events${query ? `?${query}` : ''}`, opts)) as {
		items: GuardrailEvent[] | null;
		total: number;
	};
	return { items: resp.items ?? [], total: resp.total ?? 0 };
}

/** Summarizes findings as "Category ×count, Category". */
export function findingsSummary(findings?: GuardrailFinding[] | null): string {
	if (!findings?.length) return '';
	return findings
		.map((f) => (f.count && f.count > 1 ? `${f.category} ×${f.count}` : f.category))
		.join(', ');
}
