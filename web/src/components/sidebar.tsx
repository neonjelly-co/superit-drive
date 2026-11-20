import { useQuery } from "@tanstack/react-query";
import { FolderOpen, FolderPlus, FolderUp, Plus, ShieldCheck, Trash2, Upload, Users, X } from "lucide-react";
import { type MouseEvent, useEffect, useRef } from "react";
import { api, type Place } from "../api";
import { formatCapacity, formatSize } from "../format";
import { t } from "../i18n";
import { hrefOf, type Where } from "../location";
import { foldersChoosable } from "../narrow";
import { type Chosen, chosenFolder } from "../uploads";
import { Button } from "./button";
import { LanguageSwitch } from "./language-switch";
import { Wordmark } from "./wordmark";

type Props = {
	project?: string;
	where: Where;
	go: (w: Where) => void;
	/** The shared folders the person can open, by name. */
	sharedFolders: Extract<Place, { kind: "shared-folder" }>[];
	/** True while the person is in something shared with them from a place they cannot open: "Shared with me" is where. */
	inShared?: boolean;
	/**
	 * Called with the files the person chose, or with every file of a folder they chose whole (each with the folders it
	 * is in); undefined while there is nowhere to upload to.
	 */
	onUpload?: (chosen: Chosen[]) => void;
	/** Called for "New folder"; undefined while there is no folder to make one in (such as in the trash). */
	onNewFolder?: () => void;
	/** Called for "New shared folder". */
	onNewSharedFolder: () => void;
	/** True for a project admin: "Sharing and space", what they oversee. */
	oversees?: boolean;
	/** On a phone: whether the sidebar is open over the page (from the menu button), and closing it. */
	open: boolean;
	onClose: () => void;
};

/**
 * The left sidebar of layout D: from the top, Upload (files, or a whole folder where the browser offers it) and New
 * folder, My files, the shared folders by name, New shared folder, Shared with me, Trash, for a project admin "Sharing
 * and space", the storage meter, and the switch to the other language at the bottom.
 *
 * On a phone it is behind the menu button: a drawer from the left over the page, which closes when the person goes
 * somewhere, chooses what to upload, taps outside it or presses Escape. Upload there opens the phone's own picker of
 * files and photos; a whole folder is offered only where the browser can choose one.
 */
export function Sidebar(props: Props) {
	const { project, where, go, sharedFolders, inShared, onUpload, onNewFolder, onNewSharedFolder, oversees, open, onClose } = props;
	const chooser = useRef<HTMLInputElement>(null);
	const folderChooser = useRef<HTMLInputElement>(null);
	const panel = useRef<HTMLElement>(null);
	// React does not know the attribute; the browser does.
	useEffect(() => folderChooser.current?.setAttribute("webkitdirectory", ""), []);
	// Open on a phone: the drawer takes the focus, and Escape closes it.
	useEffect(() => {
		if (!open) return;
		panel.current?.focus();
		const close = (e: KeyboardEvent) => {
			if (e.key === "Escape") onClose();
		};
		window.addEventListener("keydown", close);
		return () => window.removeEventListener("keydown", close);
	}, [open, onClose]);
	const link = (to: Where) => (e: MouseEvent) => {
		if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return; // a new tab, as the person asked
		e.preventDefault();
		go(to);
	};
	const nav = (active: boolean) =>
		`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-sm max-md:min-h-11 max-md:py-2.5 ${active ? "bg-muted font-semibold" : "hover:bg-muted"}`;
	const pick = (then: () => void) => () => {
		then();
		onClose();
	};

	return (
		<>
			{open && <div aria-hidden className="fixed inset-0 z-30 bg-foreground/30 md:hidden" onClick={onClose} />}
			<aside
				ref={panel}
				tabIndex={-1}
				aria-label={t.app.menu}
				className={`flex w-60 shrink-0 flex-col overflow-y-auto border-r border-border bg-card p-3 outline-none max-md:fixed max-md:inset-y-0 max-md:left-0 max-md:z-40 max-md:w-72 max-md:max-w-[85vw] max-md:shadow-xl max-md:transition-transform max-md:motion-reduce:transition-none ${open ? "" : "max-md:invisible max-md:-translate-x-full"}`}
			>
				<div className="flex items-start gap-2 px-2.5 py-2">
					<div className="min-w-0 flex-1">
						<Wordmark className="text-xl" />
						<p className="mt-1 truncate text-sm text-muted-foreground" title={project}>
							{project ? t.app.driveOf(project) : t.app.drive}
						</p>
					</div>
					<button
						type="button"
						onClick={onClose}
						aria-label={t.app.closeMenu}
						className="-mt-1 -mr-2 grid size-11 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted md:hidden"
					>
						<X aria-hidden className="size-5" />
					</button>
				</div>
				<div className="mt-3 grid gap-2">
					<Button variant="primary" className="justify-start" disabled={!onUpload} onClick={() => chooser.current?.click()}>
						<Upload aria-hidden /> {t.sidebar.upload}
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
							onClose();
						}}
					/>
					{foldersChoosable && (
						<>
							<Button className="justify-start" disabled={!onUpload} onClick={() => folderChooser.current?.click()}>
								<FolderUp aria-hidden /> {t.sidebar.uploadFolder}
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
									onClose();
								}}
							/>
						</>
					)}
					<Button className="justify-start" disabled={!onNewFolder} onClick={onNewFolder && pick(onNewFolder)}>
						<FolderPlus aria-hidden /> {t.sidebar.newFolder}
					</Button>
				</div>
				<nav aria-label={t.sidebar.places} className="mt-4 space-y-0.5">
					<a
						href={hrefOf({ view: "files", place: null, folder: null })}
						onClick={link({ view: "files", place: null, folder: null })}
						aria-current={where.view === "files" && where.place === null ? "page" : undefined}
						className={nav(where.view === "files" && where.place === null)}
					>
						{t.app.myFiles}
					</a>
					<h2 className="px-2.5 pt-4 pb-1 text-sm text-muted-foreground">{t.common.sharedFolders}</h2>
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
					<button type="button" onClick={pick(onNewSharedFolder)} className={`${nav(false)} text-muted-foreground`}>
						<Plus aria-hidden className="size-4 shrink-0" /> {t.sidebar.newSharedFolder}
					</button>
					<div className="pt-4" />
					<a
						href={hrefOf({ view: "shared" })}
						onClick={link({ view: "shared" })}
						aria-current={where.view === "shared" ? "page" : inShared ? "location" : undefined}
						className={nav(where.view === "shared" || !!inShared)}
					>
						<Users aria-hidden className="size-4 text-muted-foreground" /> {t.app.sharedWithMe}
					</a>
					<a
						href={hrefOf({ view: "trash" })}
						onClick={link({ view: "trash" })}
						aria-current={where.view === "trash" ? "page" : undefined}
						className={nav(where.view === "trash")}
					>
						<Trash2 aria-hidden className="size-4 text-muted-foreground" /> {t.app.trash}
					</a>
					{oversees && (
						<a
							href={hrefOf({ view: "oversight" })}
							onClick={link({ view: "oversight" })}
							aria-current={where.view === "oversight" ? "page" : undefined}
							className={nav(where.view === "oversight")}
						>
							<ShieldCheck aria-hidden className="size-4 text-muted-foreground" /> {t.app.sharingAndSpace}
						</a>
					)}
				</nav>
				<StorageMeter />
				<LanguageSwitch className="mt-2" />
			</aside>
		</>
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
	if (used === 0) words = capacity ? t.sidebar.nothingUsedOf(formatCapacity(capacity)) : t.sidebar.nothingUsed;
	else words = capacity ? t.sidebar.usedOf(formatSize(used), formatCapacity(capacity)) : t.sidebar.used(formatSize(used));
	return (
		<div className="mt-auto px-2.5 pt-4 pb-1 text-sm text-muted-foreground">
			{capacity !== undefined && (
				<div
					role="meter"
					aria-label={t.sidebar.spaceUsed}
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
			<p className="mt-1.5" title={t.sidebar.howCounted}>
				{words}
			</p>
			{/* What a mouse finds in the tooltip, a phone shows. */}
			<p className="mt-0.5 text-xs md:hidden">{t.sidebar.howCounted}</p>
			{share >= 0.9 && <p className="mt-0.5 text-attention">{t.sidebar.nearlyFull}</p>}
		</div>
	);
}
