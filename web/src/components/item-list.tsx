import { RotateCw } from "lucide-react";
import { type ReactNode, useEffect, useRef } from "react";
import { whoCanSee } from "../access";
import type { Item, Member } from "../api";
import { formatChanged, formatChangedFull, formatSize } from "../format";
import { FileIcon } from "./file-icon";
import { FileLink, FolderButton } from "./file-link";

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
	/** The items selected, by id: one shows its details beside the list; two or more turn the header into `bar`. */
	ticked: ReadonlySet<number>;
	/** Selects only this item: a click on its row. */
	onSelect: (item: Item) => void;
	/** Adds an item to the selection, or takes it out: its tick box. */
	onTick: (item: Item, on: boolean) => void;
	/** Selects every item in the list, or none: the header's tick box. */
	onTickAll: (on: boolean) => void;
	/** What the header becomes with two or more items selected: what can be done with them all. */
	bar?: ReactNode;
	/** The person looking, and the project's members, for "who can see it". */
	me?: string;
	members?: Member[];
	/** True when the person may put files and folders here: the empty list says how. */
	canAdd: boolean;
};

/**
 * The list of layout D: one row per item with a tick box, its name, who can see it in one plain sentence, when it
 * changed and its size. The click rule is the same for files and folders: clicking a name opens it (a folder opens in
 * the list; a file opens in the browser when it is a kind the browser shows, and downloads otherwise), and clicking
 * anywhere else on the row selects it alone and shows its details, and who can see it, beside the list. Tick boxes
 * select several; the header's selects them all. With two or more selected, the header becomes a bar of what can be
 * done with them together.
 */
export function ItemList(props: Props) {
	const { placeId, items, loading, error, onRetry, retryLabel, onOpen, ticked, onSelect, onTick, onTickAll, bar, me, members, canAdd } = props;
	const shown = items && placeId !== undefined && !loading && !error ? items : [];
	const tickedHere = shown.filter((i) => ticked.has(i.id)).length;
	const all = useRef<HTMLInputElement>(null);
	useEffect(() => {
		if (all.current) all.current.indeterminate = tickedHere > 0 && tickedHere < shown.length;
	}, [tickedHere, shown.length]);
	const several = tickedHere >= 2 && bar;
	return (
		<div className="mt-5 overflow-hidden rounded-xl border border-border bg-card">
			<table className="w-full table-fixed text-sm">
				{/* The widths are the columns', so the header can become the bar without the rows moving. */}
				<colgroup>
					<col className="w-11" />
					<col />
					<col className="w-56" />
					<col className="w-32" />
					<col className="w-28" />
				</colgroup>
				<thead>
					<tr className={`h-11 border-b border-border text-left ${several ? "bg-muted" : "text-muted-foreground"}`}>
						<th scope="col" className="pl-4">
							<input
								ref={all}
								type="checkbox"
								aria-label={tickedHere === shown.length && shown.length > 0 ? "Clear the selection" : "Select all"}
								title={tickedHere === shown.length && shown.length > 0 ? "Clear the selection" : "Select all"}
								checked={shown.length > 0 && tickedHere === shown.length}
								disabled={shown.length === 0}
								onChange={(e) => onTickAll(e.currentTarget.checked)}
								className="size-4 accent-primary align-middle"
							/>
						</th>
						{several ? (
							<th scope="col" colSpan={4} className="pr-2 font-normal">
								{bar}
							</th>
						) : (
							<>
								<th scope="col" className="px-3 font-normal">
									Name
								</th>
								<th scope="col" className="px-4 font-normal">
									Who can see it
								</th>
								<th scope="col" className="px-4 font-normal">
									Changed
								</th>
								<th scope="col" className="px-4 text-right font-normal">
									Size
								</th>
							</>
						)}
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
								ticked={ticked.has(item.id)}
								onOpen={onOpen}
								onSelect={onSelect}
								onTick={onTick}
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
	ticked: boolean;
	onOpen: (folder: Item) => void;
	onSelect: (item: Item) => void;
	onTick: (item: Item, on: boolean) => void;
	/** Who can see it, in one plain sentence. */
	seen: string;
};

function Row({ placeId, item, ticked, onOpen, onSelect, onTick, seen }: RowProps) {
	return (
		<tr
			// The row selects; its name opens; its tick box adds it to the selection. Keyboard: Tab reaches the tick box
			// (Space ticks it), the name (Enter opens it) and the row (Enter selects it alone).
			tabIndex={0}
			aria-selected={ticked}
			onClick={() => onSelect(item)}
			onKeyDown={(e) => {
				if (e.target === e.currentTarget && (e.key === "Enter" || e.key === " ")) {
					e.preventDefault();
					onSelect(item);
				}
			}}
			className={`cursor-default border-b border-border outline-none last:border-0 focus-visible:bg-muted ${ticked ? "bg-muted" : "hover:bg-muted/50"}`}
		>
			<td className="py-2.5 pl-4">
				<input
					type="checkbox"
					aria-label={`Select ${item.name}`}
					checked={ticked}
					onClick={(e) => e.stopPropagation()}
					onChange={(e) => onTick(item, e.currentTarget.checked)}
					className="size-4 accent-primary align-middle"
				/>
			</td>
			<td className="px-3 py-2.5">
				<span className="flex min-w-0 items-center gap-2.5">
					<FileIcon item={item} />
					{item.kind === "file" ? <FileLink placeId={placeId} item={item} /> : <FolderButton item={item} onOpen={() => onOpen(item)} />}
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
			<td colSpan={5} className="px-4 py-10 text-center text-muted-foreground">
				<div className="flex flex-col items-center">{children}</div>
			</td>
		</tr>
	);
}
