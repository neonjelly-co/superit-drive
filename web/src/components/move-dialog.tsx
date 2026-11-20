import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Folder, FolderOpen, Link2, User, Users } from "lucide-react";
import { type ReactNode, useState } from "react";
import { ROLE_NAME, whoCanSee, whoOf, whyOf } from "../access";
import { type Access, type Action, api, type FolderRef, type Item, type Member, type SeenBy } from "../api";
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
	/** What is moved: one item, or several from the same folder (the selection). */
	items: Item[];
	/** The folders from the root (or the one below the top folder) down to the one the items are in now; empty for the top folder. */
	path: FolderRef[];
	/**
	 * The shared folders they may go to besides their own place: offered only for what is in the person's own My files
	 * ("move-out"), and only those they may move things into ("move-in"). A move there comes with the warning.
	 */
	otherPlaces: PlaceRef[];
	/** The person asking, and the project's members, for the warning's sentence. */
	me?: string;
	members?: Member[];
	/** Moves it; resolves when done, rejects with an Error whose message is for the person. */
	onMove: (to: Destination) => Promise<unknown>;
	/** While several are being moved: how far it has got. */
	progress?: string;
	onClose: () => void;
};

/**
 * What the warning before a move into another place says: whether it becomes the project's, who will see what is moved
 * there (what is given there, and for a single item what was given on it too), and, for several items, what each keeps
 * that was given on it.
 */
type Warning = { becomesProjects: boolean; access: Access[]; seenBy: SeenBy; kept: { name: string; access: Access[] }[] };

/** The warning for moving items, from what the Drive says about each (GET …/move). */
async function warningFor(placeId: number, items: Item[], dest: Destination): Promise<Warning> {
	const previews = await Promise.all(items.map((i) => api.movePreview(placeId, i.id, dest.place.id, dest.folder?.id ?? null)));
	const becomesProjects = previews.some((p) => p.becomesProjects);
	const [first] = previews;
	if (first && previews.length === 1) return { access: first.access, seenBy: first.seenBy, becomesProjects, kept: [] };
	// What every item gets there is what was given there; what was given on each item it keeps, and is said apart.
	const access = (first?.access ?? []).filter((a) => !a.given);
	const everyone = access.some((a) => a.everyone);
	const link = access.some((a) => a.link);
	const members = [...new Set(access.filter((a) => a.userId && !a.former).map((a) => a.userId as string))];
	const kept = previews
		.map((p, i) => ({ name: items[i]?.name ?? "", access: p.access.filter((a) => a.given) }))
		.filter((k) => k.access.length > 0);
	return { becomesProjects, access, seenBy: { everyone, members: everyone ? [] : members, link }, kept };
}

/**
 * The Move dialog, for one item or several from the same folder: the place's folder tree, opened one level at a time
 * as the person goes down it (each level is listed from the Drive storage as it is opened), and below it the shared
 * folders the items may go to from the person's own My files. A folder cannot be moved into itself or a folder inside
 * it, so a folder being moved is shown but cannot be chosen or opened, and only folders the person may add to can be
 * chosen; for someone given part of a place, the part they were given.
 *
 * A move into a shared folder makes what is moved the project's. Before it happens the dialog warns, with who will see
 * it there as the Drive says it (who has access there, and what was given on each item itself, which it keeps), and
 * moves only when the person confirms.
 */
export function MoveDialog({ place, root, items, path, otherPlaces, me, members, onMove, progress, onClose }: Props) {
	const here = path.at(-1)?.id ?? null;
	// The way down to where the items are now starts open, so the person sees it.
	const [open, setOpen] = useState<Set<string>>(() => new Set([keyOf(place.id, root?.id ?? null), ...path.map((f) => keyOf(place.id, f.id))]));
	const [dest, setDest] = useState<Destination>();
	const [warning, setWarning] = useState<Warning>();
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
			setWarning(await warningFor(place.id, items, dest));
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
		} finally {
			setBusy(false);
		}
	};

	const chosen = dest ? keyOf(dest.place.id, dest.folder?.id ?? null) : undefined;
	const staying = dest?.place.id === place.id && (dest.folder?.id ?? null) === here;
	const node = { moving: items, here, open, toggle, chosen, choose: (p: PlaceRef, f: FolderRef | null) => setDest({ place: p, folder: f }) };
	const where = dest ? [dest.place.name, ...(dest.folder ? [dest.folder.name] : [])].join(" › ") : "";
	const one = items.length === 1 ? items[0] : undefined;
	const what = one ? `“${one.name}”` : `${items.length} items`;
	const line = (a: Access, i: number, given: string) => <AccessLine key={i} a={a} me={me} given={given} />;

	if (warning && dest) {
		return (
			<Dialog
				wide
				title={`Move ${what} to “${dest.place.name}”?`}
				description={
					warning.becomesProjects
						? one
							? `It leaves your My files and becomes the project's: the managers of “${dest.place.name}” decide who has access to it, and project admins can open it.`
							: `They leave your My files and become the project's: the managers of “${dest.place.name}” decide who has access to them, and project admins can open them.`
						: undefined
				}
				onClose={onClose}
				onSubmit={() => void move()}
				footer={
					<>
						{progress && (
							<span className="mr-auto self-center text-sm text-muted-foreground" aria-live="polite">
								{progress}
							</span>
						)}
						<Button variant="ghost" onClick={() => setWarning(undefined)} disabled={busy}>
							Back
						</Button>
						<Button variant="primary" type="submit" disabled={busy}>
							{one ? "Move it" : "Move them"}
						</Button>
					</>
				}
			>
				<p className="text-sm">
					<span className="font-semibold">
						Who will see {one ? "it" : "them"} in {where}:
					</span>{" "}
					{whoCanSee(warning.seenBy, me, members)}.
				</p>
				<ul aria-label="Who will see it there" className="mt-2 max-h-56 overflow-auto rounded-lg border border-border px-3 text-sm">
					{warning.access.map((a, i) => line(a, i, "given on it, and kept"))}
				</ul>
				{warning.kept.length > 0 && (
					<>
						<p className="mt-3 text-sm font-semibold">Some keep access given on them, so more people see those:</p>
						<ul aria-label="What each keeps" className="mt-2 max-h-56 overflow-auto rounded-lg border border-border px-3 text-sm">
							{warning.kept.map((k, i) => (
								<li key={i} className="border-b border-border py-2 last:border-0">
									<p className="font-medium break-words">{k.name}</p>
									<ul>{k.access.map((a, j) => line(a, j, "given on it, and kept"))}</ul>
								</li>
							))}
						</ul>
					</>
				)}
				<p className="mt-3 text-sm text-muted-foreground">
					{one ? "It keeps" : "Each keeps"} the access given on it and on everything in it. Access from the folders{" "}
					{one ? "it is" : "they are"} in now ends.
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
			title={`Move ${what}`}
			description={
				one
					? `Choose the folder to put ${one.kind === "folder" ? "this folder and everything in it" : "it"} in.`
					: "Choose the folder to put them in. Folders go with everything in them."
			}
			onClose={onClose}
			onSubmit={() => void submit()}
			footer={
				<>
					{progress && (
						<span className="mr-auto self-center text-sm text-muted-foreground" aria-live="polite">
							{progress}
						</span>
					)}
					<Button variant="ghost" onClick={onClose} disabled={busy}>
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
					<p className="mt-0.5 text-sm text-muted-foreground">
						{one ? "It then belongs" : "They then belong"} to the project, not to you. You see who will have access before{" "}
						{one ? "it moves" : "they move"}.
					</p>
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
	/** What is being moved: a folder among it cannot be opened or chosen. */
	moving: Item[];
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
	const isMoving = own && moving.some((m) => m.kind === "folder" && m.id === id);
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

/** One line of who will see what is moved, where it goes: who, their role, and why. */
function AccessLine({ a, me, given }: { a: Access; me?: string; given: string }) {
	const Icon = a.link ? Link2 : a.everyone ? Users : User;
	const name = whoOf(a, me);
	const why = a.given ? given : whyOf(a);
	return (
		<li className="flex items-center gap-2.5 border-b border-border py-2 last:border-0">
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
}
