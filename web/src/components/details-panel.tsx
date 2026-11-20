import { Download, ExternalLink, FolderInput, FolderOpen, MapPin, PenLine, Trash2, X } from "lucide-react";
import { type Action, api, type FolderRef, type Item } from "../api";
import { formatChangedFull, formatSize } from "../format";
import { AccessSection } from "./access-section";
import { PublicLinkSection } from "./public-link-section";
import { Button } from "./button";
import { FileIcon } from "./file-icon";

type Props = {
	placeId: number;
	/** Where it is, as the person may see it: the place's name and the folders down to the one the item is in. */
	where: string;
	item: Item;
	/** What the person may do with the item; the folder's own, for the folder being looked at. */
	can: Action[];
	/** The person asking. */
	me?: string;
	/**
	 * True for the folder being looked at itself (from "Who can see this" beside its name): only its details and who
	 * can see it, as it is open already, and changing it from inside is not offered.
	 */
	here?: boolean;
	onClose: () => void;
	onOpen: (folder: Item) => void;
	/** Rename, Move and Delete, each offered when given and the person may. */
	onRename?: () => void;
	onMove?: () => void;
	onDelete?: () => void;
	/** Goes to the folder it is in, for an item found away from it (a search). */
	onShow?: () => void;
	/** Called after who has access changed, so the list and the places follow it. */
	onAccessChanged: () => void;
	/** True while a change to the item is on its way. */
	busy?: boolean;
};

/**
 * The details panel on the right of layout D. It opens when one item is selected (a click on its row) and holds the
 * item's details, Open (a folder, or a file of a kind the browser shows) and Download, who can see it and giving
 * access (for whoever may share it), its public link (for whoever may share it), and Rename, Move and Delete.
 */
export function DetailsPanel(props: Props) {
	const { placeId, where, item, can, me, here, onClose, onOpen, onRename, onMove, onDelete, onShow, onAccessChanged, busy } = props;
	const rename = onRename && can.includes("rename");
	const move = onMove && can.includes("move");
	const remove = onDelete && can.includes("delete");
	const changes = !here && (rename || move || remove);
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
				{where && (
					<>
						<dt className="text-muted-foreground">In</dt>
						<dd className="min-w-0 break-words">{where}</dd>
					</>
				)}
				{item.addedBy && (
					<>
						<dt className="text-muted-foreground">Added by</dt>
						<dd className="min-w-0 break-words">
							A guest, <span className="font-mono">{item.addedBy.guest}</span>: from outside the project
						</dd>
					</>
				)}
			</dl>
			{!here && (
				<div className="mt-6 flex flex-wrap gap-2">
					{item.kind === "folder" ? (
						<>
							<Button onClick={() => onOpen(item)}>
								<FolderOpen aria-hidden /> Open
							</Button>
							{can.includes("download") && (
								<a href={api.zipUrl(placeId, item.id)} download className={link}>
									<Download aria-hidden className="size-4" /> Download as zip
								</a>
							)}
						</>
					) : (
						<>
							{item.opens && (
								<a href={api.openUrl(placeId, item.id)} target="_blank" rel="noopener noreferrer" className={link}>
									<ExternalLink aria-hidden className="size-4" /> Open
								</a>
							)}
							<a href={api.downloadUrl(placeId, item.id)} download className={link}>
								<Download aria-hidden className="size-4" /> Download
							</a>
						</>
					)}
					{onShow && (
						<Button onClick={onShow}>
							<MapPin aria-hidden /> Show where it is
						</Button>
					)}
				</div>
			)}

			{can.includes("see-access") && (
				<AccessSection
					key={item.id}
					placeId={placeId}
					itemId={item.id}
					what={item.kind}
					canChange={can.includes("share")}
					canInvite={can.includes("invite-guest")}
					roles={["viewer", "editor"]}
					me={me}
					onChanged={onAccessChanged}
				/>
			)}

			{can.includes("manage-link") && (
				<PublicLinkSection
					key={`link-${item.id}`}
					placeId={placeId}
					itemId={item.id}
					what={item.kind}
					canMake={can.includes("make-link")}
					onChanged={onAccessChanged}
				/>
			)}

			{changes && (
				<div className="mt-8 flex flex-wrap gap-2 border-t border-border pt-5">
					{rename && (
						<Button onClick={onRename} disabled={busy}>
							<PenLine aria-hidden /> Rename
						</Button>
					)}
					{move && (
						<Button onClick={onMove} disabled={busy}>
							<FolderInput aria-hidden /> Move
						</Button>
					)}
					{remove && (
						<Button variant="danger" onClick={onDelete} disabled={busy}>
							<Trash2 aria-hidden /> Delete
						</Button>
					)}
				</div>
			)}
			{changes && remove && (
				<p className="mt-3 text-sm text-muted-foreground">
					Deleted items go to the trash, where they can be restored for 30 days.
				</p>
			)}
		</aside>
	);
}

/** A link that looks like the interface's outline button. */
export const link =
	"inline-flex h-9 items-center gap-1.5 rounded-md border border-border bg-card px-3 text-sm font-medium outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50";

/** Where an item is, as the person may see it: the place's name (when they can open it) and the folders below it. */
export function whereOf(placeName: string | undefined, path: FolderRef[]): string {
	return [...(placeName === undefined ? [] : [placeName]), ...path.map((f) => f.name)].join(" › ");
}
