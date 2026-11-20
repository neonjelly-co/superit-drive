import { Download, ExternalLink, FolderInput, FolderOpen, MapPin, PenLine, Trash2 } from "lucide-react";
import { type Action, api, type FolderRef, type Item } from "../api";
import { formatChangedFull, formatSize } from "../format";
import { fill, t } from "../i18n";
import { AccessSection } from "./access-section";
import { Button } from "./button";
import { FileIcon } from "./file-icon";
import { PublicLinkSection } from "./public-link-section";
import { SidePanel } from "./side-panel";

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
 * The details panel on the right of layout D (on a phone, a sheet from the bottom). It opens when one item is selected
 * (a click on its row, or its ⋯ on a phone) and holds the item's details, Open (a folder, or a file of a kind the
 * browser shows) and Download, who can see it and giving access (for whoever may share it), its public link (for
 * whoever may share it), and Rename, Move and Delete.
 */
export function DetailsPanel(props: Props) {
	const { placeId, where, item, can, me, here, onClose, onOpen, onRename, onMove, onDelete, onShow, onAccessChanged, busy } = props;
	const rename = onRename && can.includes("rename");
	const move = onMove && can.includes("move");
	const remove = onDelete && can.includes("delete");
	const changes = !here && (rename || move || remove);
	return (
		<SidePanel label={t.details.label} closeLabel={t.details.close} onClose={onClose}>
			<div className="flex items-start gap-3 pr-8 max-md:pr-12">
				<FileIcon item={item} className="mt-0.5 size-6" />
				<h2 className="min-w-0 text-lg font-semibold break-words">{item.name}</h2>
			</div>
			<dl className="mt-5 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
				<dt className="text-muted-foreground">{t.details.kind}</dt>
				<dd>{item.kind === "folder" ? t.details.folder : t.details.file}</dd>
				{item.size !== undefined && (
					<>
						<dt className="text-muted-foreground">{t.common.size}</dt>
						<dd className="tabular-nums">{formatSize(item.size)}</dd>
					</>
				)}
				{item.changed && (
					<>
						<dt className="text-muted-foreground">{t.common.changed}</dt>
						<dd>{formatChangedFull(item.changed)}</dd>
					</>
				)}
				{where && (
					<>
						<dt className="text-muted-foreground">{t.details.in}</dt>
						<dd className="min-w-0 break-words">{where}</dd>
					</>
				)}
				{item.addedBy && (
					<>
						<dt className="text-muted-foreground">{t.details.addedBy}</dt>
						<dd className="min-w-0 break-words">
							{item.addedBy.guest
								? fill(t.details.addedByGuest, { address: <span className="font-mono">{item.addedBy.guest}</span> })
								: t.details.addedThroughLink}
						</dd>
					</>
				)}
			</dl>
			{!here && (
				<div className="mt-6 flex flex-wrap gap-2">
					{item.kind === "folder" ? (
						<>
							<Button onClick={() => onOpen(item)}>
								<FolderOpen aria-hidden /> {t.common.open}
							</Button>
							{can.includes("download") && (
								<a href={api.zipUrl(placeId, item.id)} download className={link}>
									<Download aria-hidden className="size-4" /> {t.common.downloadZip}
								</a>
							)}
						</>
					) : (
						<>
							{item.opens && (
								<a href={api.openUrl(placeId, item.id)} target="_blank" rel="noopener noreferrer" className={link}>
									<ExternalLink aria-hidden className="size-4" /> {t.common.open}
								</a>
							)}
							<a href={api.downloadUrl(placeId, item.id)} download className={link}>
								<Download aria-hidden className="size-4" /> {t.common.download}
							</a>
						</>
					)}
					{onShow && (
						<Button onClick={onShow}>
							<MapPin aria-hidden /> {t.details.showWhere}
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
							<PenLine aria-hidden /> {t.common.rename}
						</Button>
					)}
					{move && (
						<Button onClick={onMove} disabled={busy}>
							<FolderInput aria-hidden /> {t.common.move}
						</Button>
					)}
					{remove && (
						<Button variant="danger" onClick={onDelete} disabled={busy}>
							<Trash2 aria-hidden /> {t.common.delete}
						</Button>
					)}
				</div>
			)}
			{changes && remove && <p className="mt-3 text-sm text-muted-foreground">{t.details.deleteNote}</p>}
		</SidePanel>
	);
}

/** A link that looks like the interface's outline button (44 px tall on a phone). */
export const link =
	"inline-flex h-9 items-center gap-1.5 rounded-md border border-border bg-card px-3 text-sm font-medium outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50 max-md:h-11";

/** Where an item is, as the person may see it: the place's name (when they can open it) and the folders below it. */
export function whereOf(placeName: string | undefined, path: FolderRef[]): string {
	return [...(placeName === undefined ? [] : [placeName]), ...path.map((f) => f.name)].join(" › ");
}
