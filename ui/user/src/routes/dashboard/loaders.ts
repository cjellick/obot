// Data for the Dashboard hub's headline band and area cards.
import { AdminService, UserService } from '$lib/services';
import { getInfrastructure } from '$lib/services/admin/infrastructure';
import { topServersFromStats } from '$lib/services/dashboard/utils';
import { listMCPServerRequests } from '$lib/services/mcpserverrequests';
import {
	getOverview,
	getSupervisorInsights,
	getSupervisorStatus,
	listSessions,
	riskLevel
} from '$lib/services/provenance';

const DAY_MS = 86_400_000;
export const HIGH_RISK = 60;
const quiet = { dontLogErrors: true };

export function lastDays(days: number, now = new Date()) {
	return { start: new Date(now.getTime() - days * DAY_MS), end: now };
}

/** Settled value, or undefined when the request failed. */
function valueOf<T>(r: PromiseSettledResult<T>): T | undefined {
	return r.status === 'fulfilled' ? r.value : undefined;
}

/** Throws the first failure when every request failed, so the card reports it. */
function requireOne(results: PromiseSettledResult<unknown>[]) {
	if (results.every((r) => r.status === 'rejected')) {
		throw (results[0] as PromiseRejectedResult).reason;
	}
}

// LLM gateway

export interface LLMSummary {
	spend: number;
	requests: number;
	successRate?: number;
	topModel?: { model: string; spend: number; requests: number };
	buckets: number[];
}

export async function loadLLM(): Promise<LLMSummary> {
	const stats = await AdminService.getTokenUsageStats(lastDays(7), {}, quiet);
	const top = stats.topModelsBySpend?.[0] ?? stats.topModelsByRequests?.[0];
	return {
		spend: stats.totals.totalSpend,
		requests: stats.totals.requests,
		successRate:
			stats.totals.requests > 0 ? stats.totals.successes / stats.totals.requests : undefined,
		topModel: top ? { model: top.model, spend: top.totalSpend, requests: top.requests } : undefined,
		buckets: (stats.buckets ?? []).map((b) => b.requests)
	};
}

// MCP

export interface MCPUsageSummary {
	toolCalls: number;
	uniqueUsers: number;
	topServer?: { name: string; count: number };
}

export async function loadMCPUsage(): Promise<MCPUsageSummary> {
	const { start, end } = lastDays(7);
	const stats = await UserService.listMcpAuditLogUsageStats(
		{ start_time: start.toISOString(), end_time: end.toISOString() },
		quiet
	);
	const top = topServersFromStats(stats)[0];
	return {
		toolCalls: stats.totalCalls ?? 0,
		uniqueUsers: stats.uniqueUsers ?? 0,
		topServer: top ? { name: top.serverName, count: top.count } : undefined
	};
}

export interface MCPInventorySummary {
	vmcps?: number;
	catalogEntries?: number;
}

export async function loadMCPInventory(allVMCPs: boolean): Promise<MCPInventorySummary> {
	const results = await Promise.allSettled([
		UserService.listVMCPs({ all: allVMCPs, minimal: true, ...quiet }),
		UserService.listMCPs({ minimal: true, ...quiet })
	]);
	requireOne(results);
	const [vmcps, entries] = results;
	return { vmcps: valueOf(vmcps)?.length, catalogEntries: valueOf(entries)?.length };
}

// Supervisor

export interface SupervisorSummary {
	pendingApprovals?: number;
	sessions?: number;
	highRisk?: number;
	allowed: number;
	denied: number;
	shadowed: number;
	enabled: boolean;
}

export async function loadSupervisor(): Promise<SupervisorSummary> {
	const { start } = lastDays(7);
	const startTime = start.toISOString();
	const [overview, status, sessions, risky] = await Promise.allSettled([
		getOverview(7 * 24, quiet),
		getSupervisorStatus(quiet),
		listSessions({ startTime, limit: 1 }, quiet),
		listSessions({ startTime, minRisk: HIGH_RISK, limit: 1 }, quiet)
	]);
	if (overview.status === 'rejected') throw overview.reason;
	const o = overview.value;
	const s = valueOf(status);
	return {
		pendingApprovals: s?.pendingApprovals ?? o.decisions.approvals_pending,
		sessions: valueOf(sessions)?.total ?? o.coverage.sessions,
		highRisk:
			valueOf(risky)?.total ?? o.riskiest.filter((r) => riskLevel(r.state.risk) === 'error').length,
		allowed: o.decisions.allowed,
		denied: o.decisions.denied,
		shadowed: o.decisions.shadowed,
		enabled: s?.enabled ?? o.supervisor.enabled
	};
}

export async function loadBriefing(): Promise<string> {
	const insights = await getSupervisorInsights(false, quiet);
	return insights.briefing.trim();
}

// Connect to Obot

export interface RequestsSummary {
	pending: number;
	approved: number;
	total: number;
}

export async function loadServerRequests(): Promise<RequestsSummary> {
	const requests = await listMCPServerRequests({}, quiet);
	return {
		pending: requests.filter((r) => r.state === 'pending').length,
		approved: requests.filter((r) => r.state === 'approved').length,
		total: requests.length
	};
}

// Skills

export interface SkillsSummary {
	skills: number;
	sources: number;
	invalid: number;
}

export async function loadSkills(all: boolean): Promise<SkillsSummary> {
	const skills = all
		? await AdminService.listAllSkills(quiet)
		: await UserService.listSkills(quiet);
	const live = skills.filter((s) => !s.deleted);
	return {
		skills: live.length,
		sources: new Set(live.map((s) => s.repoURL || s.repoID).filter(Boolean)).size,
		invalid: live.filter((s) => s.valid === false || !!s.validationError).length
	};
}

// Claude environments

export interface ClaudeSummary {
	environments?: number;
	running?: number;
	sessions?: number;
	blockedEgress?: number;
}

export async function loadClaude(admin: boolean): Promise<ClaudeSummary> {
	const { start, end } = lastDays(7);
	const results = await Promise.allSettled([
		UserService.getClaudeConfig(quiet),
		UserService.listClaudeSessions({ all: admin, ...quiet }),
		admin
			? AdminService.listEgressAuditLogs(
					{
						outcome: 'denied',
						startTime: start.toISOString(),
						endTime: end.toISOString(),
						limit: 1
					},
					quiet
				)
			: Promise.reject(new Error('not available'))
	]);
	requireOne(results.slice(0, 2));
	const [config, sessions, egress] = results;
	const list = valueOf(sessions);
	return {
		environments: valueOf(config)?.environments?.length,
		running: list?.filter((s) => s.status.state === 'RUNNING' && !s.spec.claudeDone).length,
		sessions: list?.length,
		blockedEgress: valueOf(egress)?.total
	};
}

// Infrastructure

export interface InfraSummary {
	kubernetes: boolean;
	message?: string;
	errors: number;
	warnings: number;
	firstProblem?: { message: string; name: string };
	healthy: number;
	starting: number;
	failing: number;
	stopped: number;
	total: number;
}

export async function loadInfrastructure(): Promise<InfraSummary> {
	const infra = await getInfrastructure(quiet);
	const workloads = [infra.obot, infra.mcpServers, infra.claude].flatMap((s) =>
		s?.enabled ? (s.workloads ?? []) : []
	);
	const count = (h: string) => workloads.filter((w) => w.health === h).length;
	const problems = infra.problems ?? [];
	const first = problems.find((p) => p.severity === 'error') ?? problems[0];
	return {
		kubernetes: infra.kubernetes,
		message: infra.message,
		errors: problems.filter((p) => p.severity === 'error').length,
		warnings: problems.filter((p) => p.severity === 'warning').length,
		firstProblem: first ? { message: first.message, name: first.name } : undefined,
		healthy: count('healthy'),
		starting: count('starting'),
		failing: count('failing'),
		stopped: count('stopped'),
		total: workloads.length
	};
}

// Devices

export interface DevicesSummary {
	devices: number;
	users: number;
	clients: number;
	mcpServers: number;
	skills: number;
	topClient?: { name: string; devices: number };
}

export async function loadDevices(): Promise<DevicesSummary> {
	const { start, end } = lastDays(30);
	const stats = await AdminService.getDeviceScanStats(
		{ start: start.toISOString(), end: end.toISOString() },
		quiet
	);
	const top = [...(stats.clients ?? [])].sort((a, b) => b.deviceCount - a.deviceCount)[0];
	return {
		devices: stats.deviceCount ?? 0,
		users: stats.userCount ?? 0,
		clients: stats.clients?.length ?? 0,
		mcpServers: stats.mcpServers?.length ?? 0,
		skills: stats.skills?.length ?? 0,
		topClient: top ? { name: top.name, devices: top.deviceCount } : undefined
	};
}

// Identity

export interface UsersSummary {
	users: number;
	active: number;
	newUsers: number;
}

export async function loadUsers(): Promise<UsersSummary> {
	const users = await UserService.listUsers(quiet);
	const { start } = lastDays(7);
	const since = start.getTime();
	return {
		users: users.length,
		active: users.filter((u) => u.lastActiveDay && new Date(u.lastActiveDay).getTime() >= since)
			.length,
		newUsers: users.filter((u) => u.created && new Date(u.created).getTime() >= since).length
	};
}
