import type { Item } from "../api";

/**
 * The details panel on the right of layout D. It opens when exactly one item is selected and will hold the item's
 * details, "who can see this", giving access, the public link, Move and Delete. Nothing can be selected yet (that
 * comes with sharing), so for now the panel stays closed and this is the place it goes.
 */
export function DetailsPanel({ item }: { item: Item | null }) {
	if (!item) return null;
	return (
		<aside aria-label="Details" className="w-96 shrink-0 overflow-auto border-l border-border bg-card p-6">
			<h2 className="truncate text-lg font-semibold">{item.name}</h2>
		</aside>
	);
}
