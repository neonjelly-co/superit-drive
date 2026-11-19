import { useQuery } from "@tanstack/react-query";
import { FolderOpen, FolderPlus, FolderUp, Plus, Trash2, Upload } from "lucide-react";
import { type MouseEvent, useEffect, useRef } from "react";
import { api, type Place } from "../api";
import { type Chosen, chosenFolder } from "../uploads";
import { formatCapacity, formatSize } from "../format";
import { hrefOf, type Where } from "../location";
import { Button } from "./button";
import { Wordmark } from "./wordmark";

type Props = {
	project?: string;
	where: Where;
	go: (w: Where) => void;
	/** The shared folders the person can open, by name. */
	sharedFolders: Extract<Place, { kind: "shared-folder" }>[];
	/**
	 * Called with the files the person chose, or with every file of a folder they chose whole (each with the folders it
	 * is in); undefined while there is nowhere to upload to.
	 */
	onUpload?: (chosen: Chosen[]) => void;
	/** Called for "New folder"; undefined while there is no folder to make one in (such as in the trash). */
	onNewFolder?: () => void;
	/** Called for "New shared folder". */
	onNewSharedFolder: () => void;
};

/** Whether this browser lets a person choose a whole folder to upload (an input with webkitdirectory). */
const foldersChoosable = typeof document !== "undefined" && "webkitdirectory" in document.createElement("input");

/**
 * The left sidebar of layout D. In the full design it holds, from the top: Upload (files, or a whole folder where the
 * browser offers it) and New folder, My files, the shared folders by name, New shared folder, Shared with me, Trash,
 * and the storage meter at the bottom. Each arrives with the ticket that makes it work; "Shared with me" is still to
 * come.
 */
export function Sidebar({ project, where, go, sharedFolders, onUpload, onNewFolder, onNewSharedFolder }: Props) {
	const chooser = useRef<HTMLInputElement>(null);
	const folderChooser = useRef<HTMLInputElement>(null);
	// React does not know the attribute; the browser does.
	useEffect(() => folderChooser.current?.setAttribute("webkitdirectory", ""), []);
	const link = (to: Where) => (e: MouseEvent) => {
		if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return; // a new tab, as the person asked
		e.preventDefault();
		go(to);
	};
	const nav = (active: boolean) =>
		`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-sm ${active ? "bg-muted font-semibold" : "hover:bg-muted"}`;

	return (
		<aside className="flex w-60 shrink-0 flex-col border-r border-border bg-card p-3">
			<div className="px-2.5 py-2">
				<Wordmark className="text-xl" />
				<p className="mt-1 truncate text-sm text-muted-foreground" title={project}>
					Drive{project ? ` · ${project}` : ""}
				</p>
			</div>
			<div className="mt-3 grid gap-2">
				<Button variant="primary" className="justify-start" disabled={!onUpload} onClick={() => chooser.current?.click()}>
					<Upload aria-hidden /> Upload
				</Button>
				<input
					ref={chooser}
					type="file"
					multiple
					hidden
					tabIndex={-1}
					onChange={(e) => {
						const files = Array.from(e.currentTarget.files ?? []);
						e.currentTarget.value = ""; // so choosing the same file again is noticed
						if (files.length > 0) onUpload?.(files.map((file) => ({ file, folders: [] })));
					}}
				/>
				{foldersChoosable && (
					<>
						<Button className="justify-start" disabled={!onUpload} onClick={() => folderChooser.current?.click()}>
							<FolderUp aria-hidden /> Upload folder
						</Button>
						<input
							ref={folderChooser}
							type="file"
							multiple
							hidden
							tabIndex={-1}
							onChange={(e) => {
								const files = Array.from(e.currentTarget.files ?? []);
								e.currentTarget.value = "";
								if (files.length > 0) onUpload?.(chosenFolder(files));
							}}
						/>
					</>
				)}
				<Button className="justify-start" disabled={!onNewFolder} onClick={onNewFolder}>
					<FolderPlus aria-hidden /> New folder
				</Button>
			</div>
			<nav aria-label="Places" className="mt-4 space-y-0.5">
				<a
					href={hrefOf({ view: "files", place: null, folder: null })}
					onClick={link({ view: "files", place: null, folder: null })}
					aria-current={where.view === "files" && where.place === null ? "page" : undefined}
					className={nav(where.view === "files" && where.place === null)}
				>
					My files
				</a>
				<h2 className="px-2.5 pt-4 pb-1 text-sm text-muted-foreground">Shared folders</h2>
				{sharedFolders.map((f) => {
					const here = where.view === "files" && where.place === f.id;
					return (
						<a
							key={f.id}
							href={hrefOf({ view: "files", place: f.id, folder: null })}
							onClick={link({ view: "files", place: f.id, folder: null })}
							aria-current={here ? "page" : undefined}
							className={nav(here)}
							title={f.name}
						>
							<FolderOpen aria-hidden className="size-4 shrink-0 text-muted-foreground" />
							<span className="truncate">{f.name}</span>
						</a>
					);
				})}
				<button type="button" onClick={onNewSharedFolder} className={`${nav(false)} text-muted-foreground`}>
					<Plus aria-hidden className="size-4 shrink-0" /> New shared folder
				</button>
				<div className="pt-4" />
				<a
					href={hrefOf({ view: "trash" })}
					onClick={link({ view: "trash" })}
					aria-current={where.view === "trash" ? "page" : undefined}
					className={nav(where.view === "trash")}
				>
					<Trash2 aria-hidden className="size-4 text-muted-foreground" /> Trash
				</a>
			</nav>
			<StorageMeter />
		</aside>
	);
}

/**
 * About how much space the Drive uses: the Drive's own sum of the file sizes it knows, the trash included, against the
 * storage's size when the Drive was told it. Without a size, only the figure.
 */
function StorageMeter() {
	const usage = useQuery({ queryKey: ["usage"], queryFn: api.usage });
	if (!usage.data) return <div className="mt-auto" />;
	const { used, capacity } = usage.data;
	const share = capacity ? Math.min(1, used / capacity) : 0;
	let words: string;
	if (used === 0) words = capacity ? `Nothing used yet of ${formatCapacity(capacity)}` : "Nothing used yet";
	else words = capacity ? `About ${formatSize(used)} of ${formatCapacity(capacity)} used` : `About ${formatSize(used)} used`;
	return (
		<div className="mt-auto px-2.5 pt-4 pb-1 text-sm text-muted-foreground">
			{capacity !== undefined && (
				<div
					role="meter"
					aria-label="Space used"
					aria-valuemin={0}
					aria-valuemax={capacity}
					aria-valuenow={Math.min(used, capacity)}
					aria-valuetext={words}
					className="h-1.5 overflow-hidden rounded-full bg-muted"
				>
					<div
						className={`h-full ${share >= 0.9 ? "bg-attention" : "bg-foreground/60"}`}
						style={{ width: `${Math.max(share * 100, used > 0 ? 1 : 0)}%` }}
					/>
				</div>
			)}
			<p className="mt-1.5" title="The Drive adds up the sizes of the files it knows, the trash included.">
				{words}
			</p>
			{share >= 0.9 && <p className="mt-0.5 text-attention">The storage is nearly full.</p>}
		</div>
	);
}
