// The LLM gateway used to have its own section under /llm-gateway. Its pages now live under
// Models, Guardrails, Usage and Audit Logs; this maps every old URL to its new home.

function withView(path: string, view: string, search: URLSearchParams) {
	const params = new URLSearchParams(search);
	params.set('view', view);
	return `${path}?${params}`;
}

function withSearch(path: string, search: URLSearchParams) {
	const query = search.toString();
	return query ? `${path}?${query}` : path;
}

function encodePath(segments: string[]) {
	return segments.map(encodeURIComponent).join('/');
}

export function legacyGatewayLocation(path: string, search = new URLSearchParams()): string {
	const [section = '', ...rest] = path.split('/').filter(Boolean);
	const detail = encodePath(rest);

	switch (section) {
		case 'overview':
			return withView('/usage', 'gateway', search);
		case 'models':
			return withView('/models', 'models', search);
		case 'providers':
			return detail
				? withSearch(`/models/providers/${detail}`, search)
				: withView('/models', 'providers', search);
		case 'budgets':
			return detail
				? withSearch(`/models/budgets/${detail}`, search)
				: withView('/models', 'budgets', search);
		case 'routing':
			return withView('/models', 'routing', search);
		case 'settings':
			return withView('/models', 'settings', search);
		case 'logs':
			return withView('/audit-logs', 'gateway', search);
		case 'guardrails': {
			const [kind = '', ...id] = rest;
			const target = encodePath(id);
			if (kind === 'profiles') {
				return target
					? withSearch(`/guardrails/detectors/${target}`, search)
					: withView('/guardrails', 'detectors', search);
			}
			if (kind === 'events') {
				return withSearch('/guardrail-events', search);
			}
			if (kind === 'rules' && target) {
				return withSearch(`/models/guardrails/${target}`, search);
			}
			return withView('/models', 'guardrails', search);
		}
		default:
			return withView('/models', 'providers', search);
	}
}
