import { Download, FolderInput, FolderOpen, PenLine, Trash2, X } from "lucide-react";
import { type Action, api, type FolderRef, type Item } from "../api";
import { formatChangedFull, formatSize } from "../format";
import { Button } from "./button";
import { FileIcon } from "./file-icon";

type Props = {
	placeId: number;
	/** The place's name as people see it: "My files", or a shared folder's name. */
	placeName: string;
	/** What the person may do in the place. */
	can: Action[];
	item: Item;
	/** The folders from the top down to the one the item is in; empty for the top folder. */
	path: FolderRef[];
	onClose: () => void;
	onOpen: (folder: Item) => void;
	onRename: () => void;
	onMove: () => void;
	onDelete: () => void;
	/** True while a change to the item is on its way. */
	busy?: boolean;
};

/**
 * The details panel on the right of layout D. It opens when one item is selected (a click on its row) and holds the
 * item's details, and Rename, Move and Delete. "Who can see this", giving access and the public link come with
 * sharing.
 */
export function DetailsPanel({ placeId, placeName, can, item, path, onClose, onOpen, onRename, onMove, onDelete, busy }: Props) {
	const where = [placeName, ...path.map((f) => f.name)].join(" › ");
	const changes = can.includes("rename") || can.includes("move") || can.includes("delete");
	return (
		<aside aria-label="Details" className="relative w-96 shrink-0 overflow-auto border-l border-border bg-card p-6 pb-24">
			<button
				type="button"
				onClick={onClose}
				aria-label="Close details"
				className="absolute top-4 right-4 grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
			>
				<X aria-hidden className="size-4" />
			</button>
			<div className="flex items-start gap-3 pr-8">
				<FileIcon item={item} className="mt-0.5 size-6" />
				<h2 className="min-w-0 text-lg font-semibold break-words">{item.name}</h2>
			</div>
			<dl className="mt-5 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
				<dt className="text-muted-foreground">Kind</dt>
				<dd>{item.kind === "folder" ? "Folder" : "File"}</dd>
				{item.size !== undefined && (
					<>
						<dt className="text-muted-foreground">Size</dt>
						<dd className="tabular-nums">{formatSize(item.size)}</dd>
					</>
				)}
				{item.changed && (
					<>
						<dt className="text-muted-foreground">Changed</dt>
						<dd>{formatChangedFull(item.changed)}</dd>
					</>
				)}
				<dt className="text-muted-foreground">In</dt>
				<dd className="min-w-0 break-words">{where}</dd>
			</dl>
			<div className="mt-6">
				{item.kind === "folder" ? (
					<Button onClick={() => onOpen(item)}>
						<FolderOpen aria-hidden /> Open
					</Button>
				) : (
					<a
						href={api.downloadUrl(placeId, item.id)}
						download
						className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border bg-card px-3 text-sm font-medium outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50"
					>
						<Download aria-hidden className="size-4" /> Download
					</a>
				)}
			</div>
			{changes && (
				<div className="mt-8 flex flex-wrap gap-2 border-t border-border pt-5">
					{can.includes("rename") && (
						<Button onClick={onRename} disabled={busy}>
							<PenLine aria-hidden /> Rename
						</Button>
					)}
					{can.includes("move") && (
						<Button onClick={onMove} disabled={busy}>
							<FolderInput aria-hidden /> Move
						</Button>
					)}
					{can.includes("delete") && (
						<Button variant="danger" onClick={onDelete} disabled={busy}>
							<Trash2 aria-hidden /> Delete
						</Button>
					)}
				</div>
			)}
			{can.includes("delete") && (
				<p className="mt-3 text-sm text-muted-foreground">
					Deleted items go to the trash, where they can be restored for 30 days.
				</p>
			)}
		</aside>
	);
}
