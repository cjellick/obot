import { doGet, type Fetcher } from '../http';

export type InfraHealth = 'healthy' | 'starting' | 'failing' | 'stopped';

export interface InfraEvent {
	type: string;
	reason: string;
	message: string;
	count?: number;
	object: string;
	lastSeen?: string;
}

export interface InfraSession {
	id: string;
	name?: string;
	userID?: string;
	userName?: string;
	state?: string;
	created: string;
}

export interface InfraPod {
	name: string;
	phase: string;
	status: string;
	ready: string;
	restarts: number;
	lastTermination?: string;
	message?: string;
	node?: string;
	created: string;
	images?: string[];
	sessions?: InfraSession[];
}

export interface InfraWorkload {
	name: string;
	namespace: string;
	displayName?: string;
	kind: 'obot' | 'mcpServer' | 'vmcp' | 'system' | 'claudeWorkerPool' | string;
	health: InfraHealth;
	healthReason?: string;
	replicas: number;
	readyReplicas: number;
	restarts: number;
	image?: string;
	cpuRequest?: string;
	memoryRequest?: string;
	cpuLimit?: string;
	memoryLimit?: string;
	created: string;
	mcpServerID?: string;
	catalogEntryID?: string;
	catalogEntryName?: string;
	vmcpID?: string;
	vmcpName?: string;
	powerUserWorkspaceID?: string;
	userID?: string;
	userName?: string;
	environmentID?: string;
	environmentName?: string;
	link?: string;
	warningEvents: number;
	pods: InfraPod[];
	events: InfraEvent[];
}

export interface InfraProblem {
	severity: 'error' | 'warning';
	area: 'obot' | 'mcp' | 'claude';
	name: string;
	workload?: string;
	namespace?: string;
	message: string;
	link?: string;
}

export interface InfraSection {
	namespace?: string;
	enabled: boolean;
	error?: string;
	workloads: InfraWorkload[];
}

export interface Infrastructure {
	engine: string;
	kubernetes: boolean;
	message?: string;
	generatedAt: string;
	problems: InfraProblem[];
	obot: InfraSection;
	mcpServers: InfraSection;
	claude: InfraSection;
}

export interface ObotLogLine {
	seq: number;
	time: string;
	stream: string;
	level?: string;
	message?: string;
	text: string;
}

export interface ObotLogs {
	pod?: string;
	lines: ObotLogLine[];
	matched: number;
	buffered: number;
	capacity: number;
	oldestAt?: string;
}

export interface PodLogs {
	deployment: string;
	pod: string;
	container: string;
	previous: boolean;
	lines: string[];
}

function queryString(values: Record<string, string | undefined>) {
	const params = new URLSearchParams();
	for (const [key, value] of Object.entries(values)) {
		if (value) params.set(key, value);
	}
	const s = params.toString();
	return s ? `?${s}` : '';
}

export async function getInfrastructure(opts?: { fetch?: Fetcher; dontLogErrors?: boolean }) {
	return (await doGet('/infrastructure', opts)) as Infrastructure;
}

export async function getMCPServerWorkload(name: string, opts?: { fetch?: Fetcher }) {
	return (await doGet(
		`/infrastructure/mcp-servers/${encodeURIComponent(name)}`,
		opts
	)) as InfraWorkload;
}

export async function getMCPServerLogs(
	name: string,
	query: { tail?: number; previous?: boolean; query?: string; pod?: string },
	opts?: { fetch?: Fetcher }
) {
	const params = queryString({
		tail: query.tail ? String(query.tail) : undefined,
		previous: query.previous ? 'true' : undefined,
		query: query.query || undefined,
		pod: query.pod || undefined
	});
	return (await doGet(
		`/infrastructure/mcp-servers/${encodeURIComponent(name)}/logs${params}`,
		opts
	)) as PodLogs;
}

export async function getObotLogs(
	query: { query?: string; level?: string; since?: string; limit?: number },
	opts?: { fetch?: Fetcher }
) {
	const params = queryString({
		query: query.query || undefined,
		level: query.level || undefined,
		since: query.since || undefined,
		limit: query.limit ? String(query.limit) : undefined
	});
	return (await doGet(`/infrastructure/logs${params}`, opts)) as ObotLogs;
}
