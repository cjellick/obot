// Independently loaded pieces of the Dashboard hub. A 401/402/403/404 hides the card (the viewer
// can't use that area); any other failure keeps the card with its links but no numbers.
import { getHttpStatusCode } from '$lib/errors';

export type LoadStatus = 'off' | 'loading' | 'ready' | 'error' | 'hidden';

const HIDDEN_STATUS_CODES = [401, 402, 403, 404];

/** One independently loaded piece of the hub. */
export class HubLoad<T> {
	status = $state<LoadStatus>('loading');
	data = $state.raw<T>();
	#fn: () => Promise<T>;

	constructor(fn: () => Promise<T>, enabled = true) {
		this.#fn = fn;
		if (!enabled) this.status = 'off';
	}

	get visible() {
		return this.status !== 'off' && this.status !== 'hidden';
	}

	async load() {
		if (this.status === 'off') return;
		this.status = 'loading';
		try {
			this.data = await this.#fn();
			this.status = 'ready';
		} catch (err) {
			const code = getHttpStatusCode(err);
			this.status = code && HIDDEN_STATUS_CODES.includes(code) ? 'hidden' : 'error';
		}
	}
}

/** The status of a card fed by several loads: hidden only when all of them are. */
export function combinedStatus(...loads: HubLoad<unknown>[]): LoadStatus {
	const visible = loads.filter((l) => l.visible);
	if (visible.length === 0) {
		return loads.every((l) => l.status === 'off') ? 'off' : 'hidden';
	}
	if (visible.some((l) => l.status === 'loading')) return 'loading';
	if (visible.every((l) => l.status === 'error')) return 'error';
	return 'ready';
}
