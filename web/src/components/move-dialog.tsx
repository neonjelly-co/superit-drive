import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Folder, FolderOpen, User, Users } from "lucide-react";
import { type ReactNode, useState } from "react";
import { nameOf, ROLE_NAME, whoCanSee, whyOf } from "../access";
import { type Action, api, type FolderRef, type Item, type Member, type MovePreview } from "../api";
import { Button } from "./button";
import { Dialog } from "./dialog";

/** A place the item can go to: its own, or a shared folder it may move into from the person's own My files. */
export type PlaceRef = { id: number; name: string };

/** Where a move goes: a folder (null: the top folder) of a place. */
export type Destination = { place: PlaceRef; folder: FolderRef | null };

type Props = {
	place: PlaceRef;
	/**
	 * Where the tree of its own place starts: the place's top folder (null), or, for someone given part of the place, the
	 * highest folder they can open.
	 */
	root: FolderRef | null;
	item: Item;
	/** The folders from the root (or the one below the top folder) down to the one the item is in now; empty for the top folder. */
	path: FolderRef[];
	/**
	 * The shared folders it may go to besides its own place: offered only for what is in the person's own My files
	 * ("move-out"), and only those they may move things into ("move-in"). A move there comes with the warning.
	 */
	otherPlaces: PlaceRef[];
	/** The person asking, and the project's members, for the warning's sentence. */
	me?: string;
	members?: Member[];
	/** Moves it; resolves when done, rejects with an Error whose message is for the person. */
	onMove: (to: Destination) => Promise<unknown>;
	onClose: () => void;
};

/**
 * The Move dialog: the place's folder tree, opened one level at a time as the person goes down it (each level is
 * listed from the Drive storage as it is opened), and below it the shared folders the item may go to from the
 * person's own My files. A folder cannot be moved into itself or a folder inside it, so the folder being moved is
 * shown but cannot be chosen or opened, and only folders the person may add to can be chosen; for someone given part
 * of a place, the part they were given.
 *
 * A move into a shared folder makes the item the project's. Before it happens the dialog warns, with who will see it
 * there as the Drive says it (who has access there, and what was given on the item itself, which it keeps), and moves
 * only when the person confirms.
 */
export function MoveDialog({ place, root, item, path, otherPlaces, me, members, onMove, onClose }: Props) {
	const here = path.at(-1)?.id ?? null;
	// The way down to where the item is now starts open, so the person sees it.
	const [open, setOpen] = useState<Set<string>>(() => new Set([keyOf(place.id, root?.id ?? null), ...path.map((f) => keyOf(place.id, f.id))]));
	const [dest, setDest] = useState<Destination>();
	const [warning, setWarning] = useState<MovePreview>();
	const [problem, setProblem] = useState<string>();
	const [busy, setBusy] = useState(false);

	const toggle = (key: string) =>
		setOpen((s) => {
			const next = new Set(s);
			if (next.has(key)) next.delete(key);
			else next.add(key);
			return next;
		});

	const move = async () => {
		if (!dest) return;
		setBusy(true);
		setProblem(undefined);
		try {
			await onMove(dest);
			onClose();
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
			setBusy(false);
		}
	};

	const submit = async () => {
		if (!dest) return;
		if (dest.place.id === place.id || warning) return move();
		// Into another place: first, who will see it there.
		setBusy(true);
		setProblem(undefined);
		try {
			setWarning(await api.movePreview(place.id, item.id, dest.place.id, dest.folder?.id ?? null));
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
		} finally {
			setBusy(false);
		}
	};

	const chosen = dest ? keyOf(dest.place.id, dest.folder?.id ?? null) : undefined;
	const staying = dest?.place.id === place.id && (dest.folder?.id ?? null) === here;
	const node = { moving: item, here, open, toggle, chosen, choose: (p: PlaceRef, f: FolderRef | null) => setDest({ place: p, folder: f }) };
	const where = dest ? [dest.place.name, ...(dest.folder ? [dest.folder.name] : [])].join(" › ") : "";

	if (warning && dest) {
		return (
			<Dialog
				wide
				title={`Move “${item.name}” to “${dest.place.name}”?`}
				description={
					warning.becomesProjects
						? `It leaves your My files and becomes the project's: the managers of “${dest.place.name}” decide who has access to it, and project admins can open it.`
						: undefined
				}
				onClose={onClose}
				onSubmit={() => void move()}
				footer={
					<>
						<Button variant="ghost" onClick={() => setWarning(undefined)} disabled={busy}>
							Back
						</Button>
						<Button variant="primary" type="submit" disabled={busy}>
							Move it
						</Button>
					</>
				}
			>
				<p className="text-sm">
					<span className="font-semibold">Who will see it in {where}:</span> {whoCanSee(warning.seenBy, me, members)}.
				</p>
				<ul aria-label="Who will see it there" className="mt-2 max-h-56 overflow-auto rounded-lg border border-border px-3 text-sm">
					{warning.access.map((a, i) => {
						const Icon = a.everyone ? Users : User;
						const name = a.everyone
							? "Everyone in the project"
							: a.former
								? "Someone no longer in the project"
								: a.userId === me
									? "You"
									: nameOf(a);
						const why = a.given ? "given on it, and kept" : whyOf(a);
						return (
							// One person can have several lines, from several folders: the place in the list is the key.
							<li key={i} className="flex items-center gap-2.5 border-b border-border py-2 last:border-0">
								<Icon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
								<span className="min-w-0 flex-1 truncate" title={a.email}>
									{name}
								</span>
								<span className="shrink-0 text-muted-foreground">
									{ROLE_NAME[a.role]}
									{why ? ` · ${why}` : ""}
								</span>
							</li>
						);
					})}
				</ul>
				<p className="mt-3 text-sm text-muted-foreground">
					It keeps the access given on it and on everything in it. Access it had from the folders it is in now ends.
				</p>
				{problem && (
					<p role="alert" className="mt-3 text-sm text-destructive">
						{problem}
					</p>
				)}
			</Dialog>
		);
	}

	return (
		<Dialog
			wide
			title={`Move “${item.name}”`}
			description={`Choose the folder to put ${item.kind === "folder" ? "this folder and everything in it" : "it"} in.`}
			onClose={onClose}
			onSubmit={() => void submit()}
			footer={
				<>
					<Button variant="ghost" onClick={onClose}>
						Cancel
					</Button>
					<Button variant="primary" type="submit" disabled={busy || !dest || staying}>
						{dest && dest.place.id !== place.id ? "Next" : "Move here"}
					</Button>
				</>
			}
		>
			<ul aria-label="Folders" className="max-h-72 overflow-auto rounded-lg border border-border py-1 text-sm">
				<FolderNode {...node} place={place} can="upload" folder={root} depth={0} />
			</ul>
			{otherPlaces.length > 0 && (
				<>
					<p className="mt-4 text-sm font-semibold">Or into a shared folder</p>
					<p className="mt-0.5 text-sm text-muted-foreground">It then belongs to the project, not to you. You see who will have access before it moves.</p>
					<ul aria-label="Shared folders" className="mt-2 max-h-56 overflow-auto rounded-lg border border-border py-1 text-sm">
						{otherPlaces.map((p) => (
							<FolderNode key={p.id} {...node} place={p} can="move-in" folder={null} depth={0} />
						))}
					</ul>
				</>
			)}
			{problem && (
				<p role="alert" className="mt-3 text-sm text-destructive">
					{problem}
				</p>
			)}
		</Dialog>
	);
}

/** A folder in the trees, by place and id (null: the top folder). */
const keyOf = (place: number, folder: number | null) => `${place}:${folder ?? "top"}`;

type NodeProps = {
	place: PlaceRef;
	/** What the person must be allowed to do in a folder to put the item there: upload in its own place, move-in elsewhere. */
	can: Extract<Action, "upload" | "move-in">;
	moving: Item;
	here: number | null;
	open: Set<string>;
	toggle: (key: string) => void;
	chosen: string | undefined;
	choose: (place: PlaceRef, folder: FolderRef | null) => void;
	/** null: the place's top folder. */
	folder: FolderRef | null;
	/** Whether the person may put things into it, as its folder's list said; the tree's root asks its own list. */
	canPut?: boolean;
	depth: number;
};

function FolderNode(props: NodeProps) {
	const { place, can, moving, here, open, toggle, chosen, choose, folder, depth } = props;
	const id = folder?.id ?? null;
	const key = keyOf(place.id, id);
	const own = can === "upload";
	const isMoving = own && moving.kind === "folder" && id === moving.id;
	const isOpen = open.has(key) && !isMoving;
	const listing = useQuery({
		queryKey: ["items", place.id, id],
		queryFn: () => api.list(place.id, id),
		enabled: isOpen || (depth === 0 && !isMoving),
	});
	const children = listing.data?.items.filter((i) => i.kind === "folder") ?? [];
	const name = folder?.name ?? place.name;
	const canPut = depth === 0 ? (listing.data?.can.includes(can) ?? false) : (props.canPut ?? false);
	const note = isMoving
		? "the folder being moved"
		: own && id === here
			? "where it is now"
			: !canPut && (depth === 0 ? listing.data !== undefined : true)
				? "you cannot add to it"
				: undefined;
	const Icon = isOpen ? FolderOpen : Folder;

	return (
		<li>
			<div className={`flex items-center gap-1 pr-2 ${chosen === key ? "bg-muted" : ""}`} style={{ paddingLeft: 4 + depth * 20 }}>
				<button
					type="button"
					onClick={() => toggle(key)}
					disabled={isMoving}
					aria-label={isOpen ? `Close ${name}` : `Open ${name}`}
					aria-expanded={isOpen}
					className="grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted disabled:invisible"
				>
					<ChevronRight aria-hidden className={`size-4 transition-transform ${isOpen ? "rotate-90" : ""}`} />
				</button>
				<button
					type="button"
					onClick={() => choose(place, folder)}
					disabled={isMoving || !canPut}
					aria-pressed={chosen === key}
					className={`flex min-w-0 flex-1 items-center gap-2 rounded-md py-1.5 pr-2 pl-1 text-left disabled:text-muted-foreground ${chosen === key ? "font-semibold" : "hover:bg-muted/60"}`}
				>
					<Icon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
					<span className="truncate">{name}</span>
					{note && <span className="shrink-0 text-muted-foreground">({note})</span>}
				</button>
			</div>
			{isOpen && (
				<ul>
					{listing.isPending && <Line depth={depth + 1}>Loading…</Line>}
					{listing.error && <Line depth={depth + 1}>{listing.error.message}</Line>}
					{listing.data && children.length === 0 && depth === 0 && <Line depth={depth + 1}>No folders here yet.</Line>}
					{children.map((c) => (
						<FolderNode
							key={c.id}
							{...props}
							folder={{ id: c.id, name: c.name }}
							canPut={c.can?.includes(can) ?? false}
							depth={depth + 1}
						/>
					))}
				</ul>
			)}
		</li>
	);
}

function Line({ depth, children }: { depth: number; children: ReactNode }) {
	return (
		<li className="py-1.5 pr-2 text-muted-foreground" style={{ paddingLeft: 4 + depth * 20 + 32 }}>
			{children}
		</li>
	);
}
