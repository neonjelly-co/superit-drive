import { RotateCw } from "lucide-react";
import { type ReactNode, useEffect, useRef } from "react";
import { whoCanSee } from "../access";
import type { Item, Member } from "../api";
import { formatChanged, formatChangedFull, formatSize } from "../format";
import { lang, t } from "../i18n";
import { useNarrow } from "../narrow";
import { FileIcon } from "./file-icon";
import { FileLink, FolderButton } from "./file-link";
import { PhoneList, PhoneMessage, PhoneRow } from "./phone-row";

let collator: Intl.Collator | undefined;

/** Folders first, then by name as people read it ("File 2" before "File 10"), in the page's language. */
export function inOrder<T extends { kind: string; name: string }>(items: T[]): T[] {
	collator ??= new Intl.Collator(lang, { numeric: true, sensitivity: "base" });
	const c = collator;
	return [...items].sort((a, b) => (a.kind === b.kind ? c.compare(a.name, b.name) : a.kind === "folder" ? -1 : 1));
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
	/** Selects only this item: a click on its row (on a phone, its ⋯ button). */
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
	/** On a phone: true while the person is choosing several (after "Select", or a press and hold). */
	selecting?: boolean;
	/** On a phone: a press and hold on a row starts choosing several, with it ticked. */
	onHold?: (item: Item) => void;
};

/**
 * The list of layout D: one row per item with a tick box, its name, who can see it in one plain sentence, when it
 * changed and its size. The click rule is the same for files and folders: clicking a name opens it (a folder opens in
 * the list; a file opens in the browser when it is a kind the browser shows, and downloads otherwise), and clicking
 * anywhere else on the row selects it alone and shows its details, and who can see it, beside the list. Tick boxes
 * select several; the header's selects them all. With two or more selected, the header becomes a bar of what can be
 * done with them together.
 *
 * On a phone the same list folds: each row is the name with one line under it (who can see it, and the size), and a ⋯
 * button for its details. Tick boxes show only while choosing several, after "Select" or a press and hold on a row;
 * then a tap ticks a row, and what can be done with them is in a bar at the bottom.
 */
export function ItemList(props: Props) {
	const narrow = useNarrow();
	return narrow ? <PhoneItems {...props} /> : <TableItems {...props} />;
}

function TableItems(props: Props) {
	const { placeId, items, loading, error, onRetry, retryLabel, onOpen, ticked, onSelect, onTick, onTickAll, bar, me, members, canAdd } = props;
	const shown = items && placeId !== undefined && !loading && !error ? items : [];
	const tickedHere = shown.filter((i) => ticked.has(i.id)).length;
	const all = useRef<HTMLInputElement>(null);
	useEffect(() => {
		if (all.current) all.current.indeterminate = tickedHere > 0 && tickedHere < shown.length;
	}, [tickedHere, shown.length]);
	const several = tickedHere >= 2 && bar;
	const allTicked = tickedHere === shown.length && shown.length > 0;
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
								aria-label={allTicked ? t.selection.clearSelection : t.selection.selectAll}
								title={allTicked ? t.selection.clearSelection : t.selection.selectAll}
								checked={allTicked}
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
									{t.common.name}
								</th>
								<th scope="col" className="px-4 font-normal">
									{t.list.whoCanSeeIt}
								</th>
								<th scope="col" className="px-4 font-normal">
									{t.common.changed}
								</th>
								<th scope="col" className="px-4 text-right font-normal">
									{t.common.size}
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
								<RotateCw aria-hidden className="size-4" /> {retryLabel ?? t.common.tryAgain}
							</button>
						</Message>
					) : loading || !items || placeId === undefined ? (
						<Message>{t.common.loading}</Message>
					) : items.length === 0 ? (
						<Message>{canAdd ? t.list.emptyCanAdd : t.list.empty}</Message>
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

/** The list on a phone: a name with one line under it per row, and tick boxes only while choosing several. */
function PhoneItems(props: Props) {
	const { placeId, items, loading, error, onRetry, retryLabel, onOpen, ticked, onSelect, onTick, me, members, canAdd, selecting, onHold } = props;
	if (error) {
		return (
			<PhoneMessage>
				<span className="text-destructive">{error.message}</span>
				<button
					type="button"
					onClick={onRetry}
					className="mt-3 inline-flex h-11 items-center gap-1.5 rounded-md border border-border px-3 text-foreground hover:bg-muted"
				>
					<RotateCw aria-hidden className="size-4" /> {retryLabel ?? t.common.tryAgain}
				</button>
			</PhoneMessage>
		);
	}
	if (loading || !items || placeId === undefined) return <PhoneMessage>{t.common.loading}</PhoneMessage>;
	if (items.length === 0) return <PhoneMessage>{canAdd ? t.list.emptyCanAddPhone : t.list.empty}</PhoneMessage>;
	return (
		<PhoneList>
			{inOrder(items).map((item) => {
				const outside = item.addedBy ? (item.addedBy.guest ? t.list.fromAGuest : t.list.throughALink) : "";
				const line = [outside, item.seenBy ? whoCanSee(item.seenBy, me, members) : "", item.size !== undefined ? formatSize(item.size) : ""]
					.filter(Boolean)
					.join(" · ");
				const on = ticked.has(item.id);
				return (
					<PhoneRow
						key={item.id}
						item={item}
						line={line}
						open={item.kind === "file" ? { placeId, file: item } : { folder: () => onOpen(item) }}
						onDetails={() => onSelect(item)}
						detailsLabel={t.list.details(item.name)}
						selecting={selecting}
						ticked={on}
						onTick={() => onTick(item, !on)}
						tickLabel={t.selection.selectItem(item.name)}
						onHold={onHold ? () => onHold(item) : undefined}
					/>
				);
			})}
		</PhoneList>
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
					aria-label={t.selection.selectItem(item.name)}
					checked={ticked}
					onClick={(e) => e.stopPropagation()}
					onChange={(e) => onTick(item, e.currentTarget.checked)}
					className="size-4 accent-primary align-middle"
				/>
			</td>
			<td className="px-3 py-2.5">
				<span className="flex min-w-0 items-center gap-2.5">
					<FileIcon item={item} />
					<span className="flex min-w-0 flex-col">
						{item.kind === "file" ? <FileLink placeId={placeId} item={item} /> : <FolderButton item={item} onOpen={() => onOpen(item)} />}
						<AddedFromOutside item={item} />
					</span>
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

/**
 * Who outside the project added an item, as a quiet line under its name: a guest (their address in its details), or
 * someone who sent it through an upload-only link. Members treat both as coming from outside.
 */
function AddedFromOutside({ item }: { item: Item }) {
	if (!item.addedBy) return null;
	const guest = item.addedBy.guest;
	return (
		<span title={guest ? t.list.fromAGuestTitle(guest) : t.list.throughALinkTitle} className="truncate text-xs text-muted-foreground">
			{guest ? t.list.fromAGuest : t.list.throughALink}
		</span>
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
