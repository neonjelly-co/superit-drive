import { RotateCw } from "lucide-react";
import type { ReactNode } from "react";
import { whoCanSee } from "../access";
import { api, type Item, type Member } from "../api";
import { formatChanged, formatChangedFull, formatSize } from "../format";
import { FileIcon } from "./file-icon";

const collator = new Intl.Collator("en", { numeric: true, sensitivity: "base" });

/** Folders first, then by name as people read it ("File 2" before "File 10"). */
export function inOrder<T extends { kind: string; name: string }>(items: T[]): T[] {
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
	/** What the button under an error says; "Try again" when not given. */
	retryLabel?: string;
	/** Opens a folder: clicking its name. */
	onOpen: (folder: Item) => void;
	/** The item whose details are shown, if any. */
	selected?: number;
	onSelect: (item: Item) => void;
	/** The person looking, and the project's members, for "who can see it". */
	me?: string;
	members?: Member[];
	/** True when the person may put files and folders here: the empty list says how. */
	canAdd: boolean;
};

/**
 * The list of layout D: one row per item with its name, who can see it in one plain sentence, when it changed and its
 * size. The click rule is the same for files and folders: clicking a name opens it (a folder opens in the list; a file
 * downloads, until opening files in the browser arrives), and clicking anywhere else on the row selects it and shows
 * its details, and who can see it, beside the list. A later ticket adds the tick boxes and the bulk bar that replaces
 * the header row.
 */
export function ItemList(props: Props) {
	const { placeId, items, loading, error, onRetry, retryLabel, onOpen, selected, onSelect, me, members, canAdd } = props;
	return (
		<div className="mt-5 overflow-hidden rounded-xl border border-border bg-card">
			<table className="w-full table-fixed text-sm">
				<thead>
					<tr className="h-11 border-b border-border text-left text-muted-foreground">
						<th scope="col" className="px-4 font-normal">
							Name
						</th>
						<th scope="col" className="w-56 px-4 font-normal">
							Who can see it
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
								<RotateCw aria-hidden className="size-4" /> {retryLabel ?? "Try again"}
							</button>
						</Message>
					) : loading || !items || placeId === undefined ? (
						<Message>Loading…</Message>
					) : items.length === 0 ? (
						<Message>
							{canAdd ? "Nothing here yet. Drop files here to upload, or make a folder with “New folder”." : "Nothing here yet."}
						</Message>
					) : (
						inOrder(items).map((item) => (
							<Row
								key={item.id}
								placeId={placeId}
								item={item}
								selected={item.id === selected}
								onOpen={onOpen}
								onSelect={onSelect}
								seen={item.seenBy ? whoCanSee(item.seenBy, me, members) : ""}
							/>
						))
					)}
				</tbody>
			</table>
		</div>
	);
}

type RowProps = {
	placeId: number;
	item: Item;
	selected: boolean;
	onOpen: (folder: Item) => void;
	onSelect: (item: Item) => void;
	/** Who can see it, in one plain sentence. */
	seen: string;
};

function Row({ placeId, item, selected, onOpen, onSelect, seen }: RowProps) {
	return (
		<tr
			// The row selects; its name opens. Keyboard: Tab reaches the name (Enter opens it) and the row (Enter selects).
			tabIndex={0}
			aria-selected={selected}
			onClick={() => onSelect(item)}
			onKeyDown={(e) => {
				if (e.target === e.currentTarget && (e.key === "Enter" || e.key === " ")) {
					e.preventDefault();
					onSelect(item);
				}
			}}
			className={`cursor-default border-b border-border outline-none last:border-0 focus-visible:bg-muted ${selected ? "bg-muted" : "hover:bg-muted/50"}`}
		>
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
							onClick={(e) => e.stopPropagation()}
							className="truncate font-medium underline-offset-4 hover:underline focus-visible:underline"
						>
							{item.name}
						</a>
					) : (
						<button
							type="button"
							title={`Open ${item.name}`}
							onClick={(e) => {
								e.stopPropagation();
								onOpen(item);
							}}
							className="truncate text-left font-medium underline-offset-4 hover:underline focus-visible:underline"
						>
							{item.name}
						</button>
					)}
				</span>
			</td>
			<td className="truncate px-4 py-2.5" title={seen}>
				{seen}
			</td>
			<td className="px-4 py-2.5 text-muted-foreground">
				{item.changed && (
					<time dateTime={item.changed} title={formatChangedFull(item.changed)}>
						{formatChanged(item.changed)}
					</time>
				)}
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
			<td colSpan={4} className="px-4 py-10 text-center text-muted-foreground">
				<div className="flex flex-col items-center">{children}</div>
			</td>
		</tr>
	);
}
