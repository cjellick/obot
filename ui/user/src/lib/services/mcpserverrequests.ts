import { doGet, doPost, doPut, type Fetcher } from './http';

// Users' requests to add MCP servers, usually made from their agent through Obot's built-in
// MCP server, and administrators' decisions on them.

export type MCPServerRequestState = 'pending' | 'approved' | 'denied';
export type MCPServerRequestRuntime = 'remote' | 'npx' | 'uvx' | 'containerized';

export interface MCPServerRequestConfig {
	key: string;
	description?: string;
	sensitive?: boolean;
	usage?: 'env' | 'header';
}

export interface MCPServerRequest {
	id: string;
	created: string;
	name: string;
	description?: string;
	reason?: string;
	sourceURL?: string;
	runtime: MCPServerRequestRuntime;
	remoteURL?: string;
	package?: string;
	image?: string;
	port?: number;
	path?: string;
	args?: string[];
	config?: MCPServerRequestConfig[];
	suggestedVMCPID?: string;
	/** Set when the request is for a server already in the catalog. */
	existingCatalogEntryID?: string;
	/** Shown to administrators: an existing catalog entry for the same server. */
	matchedCatalogEntryID?: string;
	requesterID: string;
	requesterName?: string;
	requesterEmail?: string;
	state: MCPServerRequestState;
	decidedBy?: string;
	decidedAt?: string;
	decisionReason?: string;
	catalogEntryID?: string;
	vmcpID?: string;
	requesterCanConnect?: boolean;
}

export interface MCPServerRequestApproval {
	vmcpID?: string;
	newVMCP?: { displayName: string; description?: string };
	grantRequesterAccess?: boolean;
	reason?: string;
}

type Opts = { fetch?: Fetcher; dontLogErrors?: boolean };

const B = '/mcp-server-requests';

export async function listMCPServerRequests(
	filters: { state?: MCPServerRequestState; mine?: boolean } = {},
	opts?: Opts
): Promise<MCPServerRequest[]> {
	const params = new URLSearchParams();
	if (filters.state) params.set('state', filters.state);
	if (filters.mine) params.set('mine', 'true');
	const query = params.toString();
	const resp = (await doGet(query ? `${B}?${query}` : B, opts)) as { items?: MCPServerRequest[] };
	return resp.items ?? [];
}

export async function getMCPServerRequest(id: string, opts?: Opts): Promise<MCPServerRequest> {
	return (await doGet(`${B}/${encodeURIComponent(id)}`, opts)) as MCPServerRequest;
}

export async function approveMCPServerRequest(
	id: string,
	approval: MCPServerRequestApproval,
	opts?: Opts
): Promise<MCPServerRequest> {
	return (await doPost(
		`${B}/${encodeURIComponent(id)}/approve`,
		approval,
		opts
	)) as MCPServerRequest;
}

export async function denyMCPServerRequest(
	id: string,
	reason: string,
	opts?: Opts
): Promise<MCPServerRequest> {
	return (await doPost(
		`${B}/${encodeURIComponent(id)}/deny`,
		{ reason },
		opts
	)) as MCPServerRequest;
}

/** How administrators hear about new requests. The webhook itself is never returned. */
export interface MCPServerRequestSettings {
	slackWebhookConfigured: boolean;
	/** Enough of the webhook to recognize it. */
	slackWebhookHint?: string;
	/** The webhook was set when Obot was deployed, so it can't be changed here. */
	setViaDeployment: boolean;
}

const SETTINGS = '/mcp-server-request-settings';

export async function getMCPServerRequestSettings(opts?: Opts): Promise<MCPServerRequestSettings> {
	return (await doGet(SETTINGS, opts)) as MCPServerRequestSettings;
}

/** Sets the Slack webhook new requests are posted to. An empty URL removes it. */
export async function updateMCPServerRequestSettings(
	slackWebhookURL: string,
	opts?: Opts
): Promise<MCPServerRequestSettings> {
	return (await doPut(SETTINGS, { slackWebhookURL }, opts)) as MCPServerRequestSettings;
}

export async function testMCPServerRequestSettings(opts?: Opts): Promise<void> {
	await doPost(`${SETTINGS}/test`, {}, opts);
}

/** How the requested server runs, for display: its URL, package or image. */
export function requestTarget(r: MCPServerRequest): string {
	if (r.existingCatalogEntryID) return r.existingCatalogEntryID;
	switch (r.runtime) {
		case 'remote':
			return r.remoteURL ?? '';
		case 'npx':
		case 'uvx':
			return `${r.runtime} ${r.package ?? ''}`.trim();
		case 'containerized':
			return [r.image, r.port ? `:${r.port}${r.path ?? ''}` : ''].join('');
	}
	return '';
}

export const BUILTIN_USER_MCP_ID = 'obot';
export const BUILTIN_ADMIN_MCP_ID = 'obot-admin';

/** The connect URL of one of Obot's built-in MCP servers. */
export function builtinMCPURL(id: string, origin: string): string {
	return `${origin.replace(/\/$/, '')}/mcp-connect/${id}`;
}

/**
 * Snippets that give users Obot's built-in MCP server in Claude. managed-mcp.json, deployed
 * by MDM, adds the server for every Claude Code user; the allowlist entry admits it where
 * Claude's managed settings restrict MCP servers.
 */
export function managedSettingsSnippets(
	origin: string
): { id: string; label: string; value: string }[] {
	const url = builtinMCPURL(BUILTIN_USER_MCP_ID, origin);
	return [
		{
			id: 'managed-mcp-json',
			label: 'managed-mcp.json',
			value: JSON.stringify({ mcpServers: { obot: { type: 'http', url } } }, null, 2)
		},
		{
			id: 'claude-managed-settings',
			label: 'Claude managed settings',
			value: JSON.stringify({ allowedMcpServers: [{ serverUrl: url }] }, null, 2)
		},
		{
			id: 'codex-managed-config',
			label: 'Codex managed config.toml',
			value: `[mcp_servers.obot]\nurl = "${url}"\n`
		}
	];
}

/** A command (no label) or a config file (labelled with its path) that adds the server. */
export type ClientSnippet = { id: string; label?: string; value: string };

/** Ways to add an Obot MCP server to each agent client a user might run. */
export function clientSnippets(url: string): {
	claude: ClientSnippet[];
	codex: ClientSnippet[];
	vscode: ClientSnippet[];
	cursor: ClientSnippet[];
} {
	return {
		claude: [
			{
				id: 'claude-code',
				value: `claude mcp add --transport http obot ${url}`
			}
		],
		codex: [
			{
				id: 'codex',
				value: `codex mcp add obot --url ${url}\ncodex mcp login obot`
			}
		],
		vscode: [
			{
				id: 'vscode-command',
				value: `code --add-mcp '${JSON.stringify({ name: 'obot', type: 'http', url })}'`
			},
			{
				id: 'vscode-json',
				label: '.vscode/mcp.json',
				value: JSON.stringify({ servers: { obot: { type: 'http', url } } }, null, 2)
			}
		],
		cursor: [
			{
				id: 'cursor-json',
				label: '~/.cursor/mcp.json',
				value: JSON.stringify({ mcpServers: { obot: { url } } }, null, 2)
			}
		]
	};
}

/** Cursor's one-click install link for an MCP server. */
export function cursorInstallLink(url: string): string {
	const config = btoa(JSON.stringify({ url }));
	return `cursor://anysphere.cursor-deeplink/mcp/install?name=obot&config=${encodeURIComponent(config)}`;
}
