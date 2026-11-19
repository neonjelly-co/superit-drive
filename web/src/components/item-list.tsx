import { RotateCw } from "lucide-react";
import type { ReactNode } from "react";
import { api, type Item } from "../api";
import { formatChanged, formatChangedFull, formatSize } from "../format";
import { FileIcon } from "./file-icon";

const collator = new Intl.Collator("en", { numeric: true, sensitivity: "base" });

/** Folders first, then by name as people read it ("File 2" before "File 10"). */
function inOrder(items: Item[]): Item[] {
	return [...items].sort((a, b) =>
		a.kind === b.kind ? collator.compare(a.name, b.name) : a.kind === "folder" ? -1 : 1,
	);
}

type Props = {
	/** The place the items are in. */
	placeId?: number;
	items?: Item[];
	loading: boolean;
	error?: Error | null;
	onRetry: () => void;
};

/**
 * The list of layout D: one row per item with its name, when it changed and its size. Clicking a file's name downloads
 * it. Later tickets open folders and files by their names, and add the tick boxes, "who can see it", and the bulk bar
 * that replaces the header row.
 */
export function ItemList({ placeId, items, loading, error, onRetry }: Props) {
	return (
		<div className="mt-5 overflow-hidden rounded-xl border border-border bg-card">
			<table className="w-full table-fixed text-sm">
				<thead>
					<tr className="h-11 border-b border-border text-left text-muted-foreground">
						<th scope="col" className="px-4 font-normal">
							Name
						</th>
						<th scope="col" className="w-32 px-4 font-normal">
							Changed
						</th>
						<th scope="col" className="w-28 px-4 text-right font-normal">
							Size
						</th>
					</tr>
				</thead>
				<tbody>
					{error ? (
						<Message>
							<span className="text-destructive">{error.message}</span>
							<button
								type="button"
								onClick={onRetry}
								className="mt-3 inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-foreground hover:bg-muted"
							>
								<RotateCw aria-hidden className="size-4" /> Try again
							</button>
						</Message>
					) : loading || !items || placeId === undefined ? (
						<Message>Loading your files…</Message>
					) : items.length === 0 ? (
						<Message>Nothing here yet. Drop files here to upload.</Message>
					) : (
						inOrder(items).map((item) => <Row key={item.id} placeId={placeId} item={item} />)
					)}
				</tbody>
			</table>
		</div>
	);
}

function Row({ placeId, item }: { placeId: number; item: Item }) {
	return (
		<tr className="border-b border-border last:border-0 hover:bg-muted/50">
			<td className="px-4 py-2.5">
				<span className="flex min-w-0 items-center gap-2.5">
					<FileIcon item={item} />
					{item.kind === "file" ? (
						// The Drive sends the file as an attachment under its original name; download makes the browser save it
						// rather than open it.
						<a
							href={api.downloadUrl(placeId, item.id)}
							download
							title={`Download ${item.name}`}
							className="truncate font-medium underline-offset-4 hover:underline focus-visible:underline"
						>
							{item.name}
						</a>
					) : (
						<span className="truncate font-medium" title={item.name}>
							{item.name}
						</span>
					)}
				</span>
			</td>
			<td className="px-4 py-2.5 text-muted-foreground">
				{item.changed && <time dateTime={item.changed} title={formatChangedFull(item.changed)}>{formatChanged(item.changed)}</time>}
			</td>
			<td className="px-4 py-2.5 text-right text-muted-foreground tabular-nums">
				{item.size !== undefined ? formatSize(item.size) : ""}
			</td>
		</tr>
	);
}

function Message({ children }: { children: ReactNode }) {
	return (
		<tr>
			<td colSpan={3} className="px-4 py-10 text-center text-muted-foreground">
				<div className="flex flex-col items-center">{children}</div>
			</td>
		</tr>
	);
}
