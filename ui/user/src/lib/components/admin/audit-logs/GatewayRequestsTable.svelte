<script lang="ts">
	import { VirtualPageTable } from '$lib/components/ui';
	import { formatCost, formatNumber, type GatewayLog } from '$lib/services/llm-gateway/gateway';
	import { providerLabel } from '$lib/services/llm-gateway/providers';
	import { formatAuditLogTableTimestamp } from '$lib/time';
	import { throttle } from '$lib/utils';
	import { GripVertical } from '@lucide/svelte';
	import { tick } from 'svelte';
	import { twMerge } from 'tailwind-merge';

	interface Props {
		onSelectRow?: (log: GatewayLog) => void;
		getUserName: (log: GatewayLog) => string;
	}

	let { onSelectRow, getUserName }: Props = $props();

	let startX = 0;
	let startWidth = 0;
	let currentCell: HTMLElement | null | undefined = undefined;
	let cellHandle: HTMLElement | null | undefined = undefined;

	let headerRowElement: HTMLElement | null | undefined = $state();

	const resizeColumn = throttle((ev: PointerEvent) => {
		const diff = ev.pageX - startX;
		const minWidth = currentCell?.getAttribute('data-min-width') ?? '0ch';

		currentCell!.style.width = `max(${minWidth}, ${startWidth + diff}px)`;
	}, 1000 / 60);

	const stopResize = async () => {
		document.removeEventListener('pointermove', resizeColumn);
		document.removeEventListener('pointerup', stopResize);

		await tick();

		cellHandle?.scrollIntoView({ block: 'nearest', inline: 'center', behavior: 'smooth' });
	};

	function statusClass(status: string) {
		return status === 'success'
			? 'bg-success/15 text-success'
			: status === 'error'
				? 'bg-error/15 text-error'
				: 'bg-warning/15 text-warning';
	}
</script>

{#snippet thResizeHandler()}
	<button
		class="resize-handle sticky right-0 ml-auto flex min-h-full cursor-col-resize items-center outline-none"
		{@attach (node) => {
			const pointerDownHandler = (ev: PointerEvent) => {
				currentCell = (ev.target as HTMLElement).closest('th');
				if (!currentCell) return;

				cellHandle = ev.currentTarget as typeof cellHandle;

				startX = ev.pageX;
				startWidth = currentCell.clientWidth;

				document.addEventListener('pointermove', resizeColumn);
				document.addEventListener('pointerup', stopResize);
			};

			node.addEventListener('pointerdown', pointerDownHandler);

			return () => {
				node.removeEventListener('pointerdown', pointerDownHandler);
			};
		}}
	>
		<GripVertical class="w-3" />
	</button>
{/snippet}

{#snippet tdResizeHandler()}
	<button
		class="resize-handle ml-auto flex min-h-full cursor-col-resize items-center opacity-0 outline-none group-hover:opacity-100"
		onclick={(ev) => ev.stopPropagation()}
		{@attach (node) => {
			const pointerDownHandler = (ev: PointerEvent) => {
				const td = (ev.target as HTMLElement).closest('td');
				if (!td) return;

				cellHandle = ev.currentTarget as typeof cellHandle;

				const row = td.closest('tr');
				if (!row) return;

				const index = Array.from(row.children).indexOf(td);

				currentCell = headerRowElement?.children.item(index) as typeof currentCell;
				if (!currentCell) return;

				startX = ev.pageX;
				startWidth = currentCell.clientWidth;

				document.addEventListener('pointermove', resizeColumn);
				document.addEventListener('pointerup', stopResize);
			};

			node.addEventListener('pointerdown', pointerDownHandler);

			return () => {
				node.removeEventListener('pointerdown', pointerDownHandler);
			};
		}}
	>
		<GripVertical class="w-3" />
	</button>
{/snippet}

{#snippet th(content: string, { class: klass = '', minWidth = '0ch' } = {})}
	<th
		class={twMerge(
			'dark:bg-base-200 bg-base-300 text-muted-content box-content w-[24ch] truncate text-left text-xs font-medium tracking-wider uppercase',
			klass
		)}
		data-min-width={minWidth}
	>
		<div class="box-content flex h-full px-6">
			<div class=" self-center py-3 text-nowrap">{content}</div>
			{@render thResizeHandler()}
		</div>
	</th>
{/snippet}

{#snippet td(content: string | number | boolean | null | undefined)}
	<td class="text-sm whitespace-nowrap">
		<div class="box-content flex h-full px-6">
			<div class="flex-1 truncate py-4">
				{content}
			</div>
			{@render tdResizeHandler()}
		</div>
	</td>
{/snippet}

{#snippet modelCell(log: GatewayLog)}
	<td class="text-sm whitespace-nowrap">
		<div class="box-content flex h-full px-6">
			<div class="flex min-w-0 flex-1 items-center gap-1 py-4">
				<span class="truncate">{log.model || '—'}</span>
				{#if (log.fallback_index ?? 0) > 0}
					<span class="bg-warning/15 text-warning shrink-0 rounded-full px-2 py-0.5 text-[11px]"
						>fallback</span
					>
				{/if}
				{#if (log.number_of_retries ?? 0) > 0}
					<span class="bg-base-300 shrink-0 rounded-full px-2 py-0.5 text-[11px]"
						>{log.number_of_retries} retries</span
					>
				{/if}
			</div>
			{@render tdResizeHandler()}
		</div>
	</td>
{/snippet}

{#snippet statusCell(status: string)}
	<td class="text-sm whitespace-nowrap">
		<div class="box-content flex h-full px-6">
			<div class="flex min-w-0 flex-1 items-center py-4">
				<span class={twMerge('rounded-full px-2 py-0.5 text-xs', statusClass(status))}
					>{status}</span
				>
			</div>
			{@render tdResizeHandler()}
		</div>
	</td>
{/snippet}

<!-- Data Table -->
<div>
	<div
		class="dark:bg-base-300 bg-base-100 flex w-full min-w-full flex-1 divide-y divide-gray-200 rounded-lg border border-transparent shadow-sm"
	>
		<VirtualPageTable class={twMerge('w-full flex-1 table-fixed border-collapse border-spacing-0')}>
			{#snippet header()}
				<thead>
					<tr bind:this={headerRowElement}>
						{@render th('Timestamp', { class: 'w-[28ch]', minWidth: '28ch' })}
						{@render th('User', { class: 'w-[24ch]', minWidth: '18ch' })}
						{@render th('Provider', { class: 'w-[18ch]', minWidth: '14ch' })}
						{@render th('Model', { class: 'w-[32ch]', minWidth: '24ch' })}
						{@render th('Status', { class: 'w-[16ch]', minWidth: '14ch' })}
						{@render th('Type', { class: 'w-[28ch]', minWidth: '20ch' })}
						{@render th('Key', { class: 'w-[22ch]', minWidth: '16ch' })}
						{@render th('Tokens', { class: 'w-[14ch]', minWidth: '12ch' })}
						{@render th('Cost', { class: 'w-[14ch]', minWidth: '12ch' })}
						{@render th('Latency (ms)', { class: 'w-[18ch]', minWidth: '16ch' })}
					</tr>
				</thead>
			{/snippet}

			{#snippet children({ items }: { items: { index: number; data: GatewayLog }[] })}
				{#each items as item (item.data.id)}
					{@const log = item.data}
					<tr
						class={twMerge(
							'group m-0 h-14 text-sm leading-0 text-[0] transition-colors duration-300',
							onSelectRow && 'hover:bg-base-200 dark:hover:bg-base-400 cursor-pointer'
						)}
						onclick={() => onSelectRow?.(log)}
					>
						{@render td(formatAuditLogTableTimestamp(log.timestamp))}
						{@render td(getUserName(log) || '—')}
						{@render td(providerLabel(log.provider))}
						{@render modelCell(log)}
						{@render statusCell(log.status)}
						{@render td(`${log.object}${log.stream ? ' · stream' : ''}`)}
						{@render td(log.selected_key_name ?? '—')}
						{@render td(formatNumber(log.token_usage?.total_tokens))}
						{@render td(formatCost(log.cost))}
						{@render td(
							log.latency ? Intl.NumberFormat().format(Math.round(log.latency * 1000)) : '—'
						)}
					</tr>
				{/each}
			{/snippet}
		</VirtualPageTable>
	</div>
</div>
