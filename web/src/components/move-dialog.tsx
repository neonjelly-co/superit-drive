import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Folder, FolderOpen } from "lucide-react";
import { type ReactNode, useState } from "react";
import { api, type FolderRef, type Item } from "../api";
import { Button } from "./button";
import { Dialog } from "./dialog";

type Props = {
	placeId: number;
	/** The place's name as people see it: "My files", or a shared folder's name. */
	placeName: string;
	item: Item;
	/** The folders from the top down to the one the item is in now; empty for the top folder. */
	path: FolderRef[];
	/** Moves it; resolves when done, rejects with an Error whose message is for the person. */
	onMove: (to: FolderRef | null) => Promise<unknown>;
	onClose: () => void;
};

/**
 * The Move dialog: the place's folder tree, opened one level at a time as the person goes down it (each level is
 * listed from the Drive storage as it is opened). A folder cannot be moved into itself or a folder inside it, so the
 * folder being moved is shown but cannot be chosen or opened. Moves stay inside the place, so its tree is all there is
 * (moving from "My files" into a shared folder comes with its warning, later).
 */
export function MoveDialog({ placeId, placeName, item, path, onMove, onClose }: Props) {
	const here = path.at(-1)?.id ?? null;
	// The way down to where the item is now starts open, so the person sees it.
	const [open, setOpen] = useState<Set<number | null>>(() => new Set([null, ...path.map((f) => f.id)]));
	const [dest, setDest] = useState<FolderRef | null | undefined>(undefined);
	const [problem, setProblem] = useState<string>();
	const [busy, setBusy] = useState(false);

	const toggle = (id: number | null) =>
		setOpen((s) => {
			const next = new Set(s);
			if (next.has(id)) next.delete(id);
			else next.add(id);
			return next;
		});

	const submit = async () => {
		if (dest === undefined) return;
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

	const chosenId = dest === undefined ? undefined : (dest?.id ?? null);
	const node = { placeId, placeName, moving: item, here, open, toggle, chosen: chosenId, choose: setDest };

	return (
		<Dialog
			wide
			title={`Move “${item.name}”`}
			description={`Choose the folder to put ${item.kind === "folder" ? "this folder and everything in it" : "it"} in.`}
			onClose={onClose}
			onSubmit={submit}
			footer={
				<>
					<Button variant="ghost" onClick={onClose}>
						Cancel
					</Button>
					<Button variant="primary" type="submit" disabled={busy || chosenId === undefined || chosenId === here}>
						Move here
					</Button>
				</>
			}
		>
			<ul aria-label="Folders" className="max-h-72 overflow-auto rounded-lg border border-border py-1 text-sm">
				<FolderNode {...node} folder={null} depth={0} />
			</ul>
			{problem && (
				<p role="alert" className="mt-3 text-sm text-destructive">
					{problem}
				</p>
			)}
		</Dialog>
	);
}

type NodeProps = {
	placeId: number;
	placeName: string;
	moving: Item;
	here: number | null;
	open: Set<number | null>;
	toggle: (id: number | null) => void;
	chosen: number | null | undefined;
	choose: (f: FolderRef | null) => void;
	/** null: the place's top folder. */
	folder: FolderRef | null;
	depth: number;
};

function FolderNode(props: NodeProps) {
	const { placeId, placeName, moving, here, open, toggle, chosen, choose, folder, depth } = props;
	const id = folder?.id ?? null;
	const isMoving = moving.kind === "folder" && id === moving.id;
	const isOpen = open.has(id) && !isMoving;
	const listing = useQuery({
		queryKey: ["items", placeId, id],
		queryFn: () => api.list(placeId, id),
		enabled: isOpen,
	});
	const children = listing.data?.items.filter((i) => i.kind === "folder") ?? [];
	const name = folder?.name ?? placeName;
	const note = isMoving ? "the folder being moved" : id === here ? "where it is now" : undefined;
	const Icon = isOpen ? FolderOpen : Folder;

	return (
		<li>
			<div
				className={`flex items-center gap-1 pr-2 ${chosen === id ? "bg-muted" : ""}`}
				style={{ paddingLeft: 4 + depth * 20 }}
			>
				<button
					type="button"
					onClick={() => toggle(id)}
					disabled={isMoving}
					aria-label={isOpen ? `Close ${name}` : `Open ${name}`}
					aria-expanded={isOpen}
					className="grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted disabled:invisible"
				>
					<ChevronRight aria-hidden className={`size-4 transition-transform ${isOpen ? "rotate-90" : ""}`} />
				</button>
				<button
					type="button"
					onClick={() => choose(folder)}
					disabled={isMoving}
					aria-pressed={chosen === id}
					className={`flex min-w-0 flex-1 items-center gap-2 rounded-md py-1.5 pr-2 pl-1 text-left disabled:text-muted-foreground ${chosen === id ? "font-semibold" : "hover:bg-muted/60"}`}
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
						<FolderNode key={c.id} {...props} folder={{ id: c.id, name: c.name }} depth={depth + 1} />
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
