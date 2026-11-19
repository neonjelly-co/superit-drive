import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Upload } from "lucide-react";
import { type DragEvent, type MouseEvent, useCallback, useEffect, useState } from "react";
import { ApiError, api, type FolderRef, type Item, type TrashItem } from "./api";
import { ConflictDialog } from "./components/conflict-dialog";
import { DetailsPanel } from "./components/details-panel";
import { ItemList } from "./components/item-list";
import { MoveDialog } from "./components/move-dialog";
import { NameDialog } from "./components/name-dialog";
import { type Notice, NoticeBar } from "./components/notice";
import { Sidebar } from "./components/sidebar";
import { TrashList } from "./components/trash-list";
import { UploadsPanel } from "./components/uploads-panel";
import { hrefOf, useWhere, type Where } from "./location";
import { freeName } from "./names";
import { droppedFiles, useUploads } from "./uploads";

const carriesFiles = (e: { dataTransfer: DataTransfer | null }) => e.dataTransfer?.types.includes("Files") ?? false;

let nextNotice = 1;

/**
 * The Drive as a member sees it: layout D of the prototype. A sidebar on the left, the list in the middle, and the
 * details panel on the right, which opens when one item is selected. The list shows a folder of "My files" (the
 * address says which) or the trash. Files are uploaded into the folder being looked at, with the sidebar's Upload
 * button or by dragging them onto the list.
 */
export function App() {
	const queryClient = useQueryClient();
	const me = useQuery({ queryKey: ["me"], queryFn: api.me });
	const places = useQuery({ queryKey: ["places"], queryFn: api.places });
	const placeId = places.data?.find((p) => p.kind === "my-files")?.id;
	const [where, go] = useWhere();
	const folderId = where.view === "files" ? where.folder : null;

	const listing = useQuery({
		queryKey: ["items", placeId, folderId],
		queryFn: () => api.list(placeId as number, folderId),
		enabled: placeId !== undefined && where.view === "files",
	});
	const trash = useQuery({
		queryKey: ["trash", placeId],
		queryFn: () => api.trash(placeId as number),
		enabled: placeId !== undefined && where.view === "trash",
	});
	const items = listing.data?.items;
	const path = listing.data?.path ?? [];
	const inFiles = where.view === "files" && placeId !== undefined && listing.data !== undefined;
	const uploads = useUploads(placeId, folderId, items);

	const [selectedId, setSelectedId] = useState<number>();
	const selected = items?.find((i) => i.id === selectedId);
	const [dialog, setDialog] = useState<"new-folder" | "rename" | "move" | null>(null);
	const [busy, setBusy] = useState(false);
	const [restoring, setRestoring] = useState<number>();
	const [notice, setNotice] = useState<Notice | null>(null);
	const [dragging, setDragging] = useState(false);
	const say = useCallback((n: Omit<Notice, "key">) => setNotice({ ...n, key: nextNotice++ }), []);
	const closeNotice = useCallback(() => setNotice(null), []);

	const goTo = useCallback(
		(w: Where) => {
			setSelectedId(undefined);
			go(w);
		},
		[go],
	);
	const openFolder = (folder: FolderRef | Item | null) => goTo({ view: "files", folder: folder?.id ?? null });

	const here = path.at(-1)?.name ?? "My files";
	const project = me.data?.project;
	useEffect(() => {
		const name = where.view === "trash" ? "Trash" : here;
		document.title = project ? `${name} · ${project}` : name;
	}, [where.view, here, project]);

	// A file dropped anywhere but the list would make the browser leave the Drive to show it. Refuse those drops.
	useEffect(() => {
		const refuse = (e: globalThis.DragEvent) => {
			if (!carriesFiles(e) || e.defaultPrevented) return;
			e.preventDefault();
			if (e.dataTransfer) e.dataTransfer.dropEffect = "none";
		};
		window.addEventListener("dragover", refuse);
		window.addEventListener("drop", refuse);
		return () => {
			window.removeEventListener("dragover", refuse);
			window.removeEventListener("drop", refuse);
		};
	}, []);

	const dragOver = (e: DragEvent) => {
		if (!carriesFiles(e) || !inFiles) return;
		e.preventDefault();
		e.dataTransfer.dropEffect = "copy";
		setDragging(true);
	};

	/** What changed in a folder: its list, and the space used. */
	const refresh = (folder: number | null) => {
		void queryClient.invalidateQueries({ queryKey: ["items", placeId, folder] });
		void queryClient.invalidateQueries({ queryKey: ["usage"] });
	};

	const remove = async (item: Item) => {
		if (placeId === undefined) return;
		setBusy(true);
		try {
			const t = await api.remove(placeId, item.id);
			setSelectedId(undefined);
			refresh(folderId);
			void queryClient.invalidateQueries({ queryKey: ["trash", placeId] });
			say({ text: `“${t.name}” was moved to the trash.`, action: { label: "Undo", run: () => void restore(t) } });
		} catch (e) {
			say({ text: e instanceof Error ? e.message : String(e), failed: true });
			refresh(folderId);
		} finally {
			setBusy(false);
		}
	};

	const restore = async (t: TrashItem) => {
		if (placeId === undefined) return;
		setRestoring(t.id);
		try {
			const r = await api.restore(placeId, t.id);
			const to = r.path.at(-1) ?? null;
			refresh(to?.id ?? null);
			void queryClient.invalidateQueries({ queryKey: ["trash", placeId] });
			const where = ["My files", ...r.path.map((f) => f.name)].join(" › ");
			const renamed = r.item.name !== t.name ? ` as “${r.item.name}”, as “${t.name}” is taken there` : "";
			say({ text: `“${t.name}” is back in ${where}${renamed}.`, action: { label: "Show", run: () => openFolder(to) } });
		} catch (e) {
			say({ text: e instanceof Error ? e.message : String(e), failed: true });
			void queryClient.invalidateQueries({ queryKey: ["trash", placeId] });
		} finally {
			setRestoring(undefined);
		}
	};

	const folderGone = listing.error instanceof ApiError && listing.error.status === 404;

	return (
		<div className="flex h-screen bg-background text-foreground">
			<Sidebar
				project={project}
				where={where}
				go={goTo}
				onUpload={inFiles ? (files) => uploads.add(files.map((file) => ({ file }))) : undefined}
				onNewFolder={inFiles ? () => setDialog("new-folder") : undefined}
			/>
			<div
				className="relative min-w-0 flex-1"
				onDragEnter={dragOver}
				onDragOver={dragOver}
				onDragLeave={(e) => {
					if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
				}}
				onDrop={(e) => {
					if (!carriesFiles(e) || !inFiles) return;
					e.preventDefault();
					setDragging(false);
					uploads.add(droppedFiles(e.dataTransfer));
				}}
			>
				<main className="h-full overflow-auto p-6 pb-24">
					{where.view === "trash" ? (
						<>
							<h1 className="text-xl font-semibold">Trash</h1>
							<p className="mt-1 text-sm text-muted-foreground">
								Deleted files and folders stay here for 30 days, and are then removed for good. Until then they count
								towards the space used.
							</p>
							<TrashList
								items={trash.data}
								loading={places.isPending || trash.isPending}
								error={places.error ?? trash.error}
								onRetry={() => (places.error ? places.refetch() : trash.refetch())}
								onRestore={(t) => void restore(t)}
								restoring={restoring}
							/>
						</>
					) : (
						<>
							<Breadcrumb path={path} go={goTo} />
							<p className="mt-1 text-sm text-muted-foreground">
								Only you can see these, unless you share them. Project admins cannot open them.
							</p>
							<ItemList
								placeId={placeId}
								items={items}
								loading={places.isPending || listing.isPending}
								error={places.error ?? listing.error}
								onRetry={() => (places.error ? places.refetch() : folderGone ? openFolder(null) : listing.refetch())}
								retryLabel={folderGone ? "Go to My files" : undefined}
								onOpen={openFolder}
								selected={selectedId}
								onSelect={(item) => setSelectedId(item.id)}
							/>
							{items && items.length > 0 && (
								<p className="mt-3 text-sm text-muted-foreground">
									Click a name to open it. Click elsewhere on a row to see its details.
								</p>
							)}
						</>
					)}
				</main>
				{dragging && (
					<div className="pointer-events-none absolute inset-3 grid place-items-center rounded-xl border-2 border-dashed border-ring bg-card/85">
						<p className="flex items-center gap-2 font-semibold">
							<Upload aria-hidden className="size-5 text-muted-foreground" /> Drop files to upload them to {here}
						</p>
					</div>
				)}
				<NoticeBar notice={notice} onClose={closeNotice} />
				<UploadsPanel uploads={uploads.uploads} onClose={uploads.clear} />
			</div>
			{selected && placeId !== undefined && where.view === "files" && (
				<DetailsPanel
					placeId={placeId}
					item={selected}
					path={path}
					busy={busy}
					onClose={() => setSelectedId(undefined)}
					onOpen={openFolder}
					onRename={() => setDialog("rename")}
					onMove={() => setDialog("move")}
					onDelete={() => void remove(selected)}
				/>
			)}

			{dialog === "new-folder" && placeId !== undefined && (
				<NameDialog
					title="New folder"
					description={`In ${["My files", ...path.map((f) => f.name)].join(" › ")}`}
					initial={freeName("New folder", items, true)}
					submitLabel="Make folder"
					onSubmit={async (name) => {
						await api.newFolder(placeId, folderId, name);
						refresh(folderId);
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{dialog === "rename" && selected && placeId !== undefined && (
				<NameDialog
					title={`Rename “${selected.name}”`}
					initial={selected.name}
					selectStem={selected.kind === "file"}
					submitLabel="Rename"
					onSubmit={async (name) => {
						if (name === selected.name) return;
						await api.rename(placeId, selected.id, name);
						refresh(folderId);
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{dialog === "move" && selected && placeId !== undefined && (
				<MoveDialog
					placeId={placeId}
					item={selected}
					path={path}
					onMove={async (to) => {
						await api.move(placeId, selected.id, to?.id ?? null);
						setSelectedId(undefined);
						refresh(folderId);
						refresh(to?.id ?? null);
						say({ text: `“${selected.name}” was moved to ${to?.name ?? "My files"}.`, action: { label: "Show", run: () => openFolder(to) } });
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{uploads.question && <ConflictDialog key={uploads.question.key} question={uploads.question} onAnswer={uploads.answer} />}
		</div>
	);
}

/** Where the list is: "My files" and the folders down to the one shown, each but the last a link up to it. */
function Breadcrumb({ path, go }: { path: FolderRef[]; go: (w: Where) => void }) {
	const link = (to: FolderRef | null) => (e: MouseEvent) => {
		if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
		e.preventDefault();
		go({ view: "files", folder: to?.id ?? null });
	};
	if (path.length === 0) return <h1 className="text-xl font-semibold">My files</h1>;
	const crumb = "max-w-64 truncate underline-offset-4 hover:underline";
	return (
		<nav aria-label="Breadcrumb">
			<ol className="flex min-w-0 flex-wrap items-center gap-1 text-xl font-semibold">
				<li>
					<a href={hrefOf({ view: "files", folder: null })} onClick={link(null)} className={`block ${crumb}`}>
						My files
					</a>
				</li>
				{path.map((f, i) => (
					<li key={f.id} className="flex min-w-0 items-center gap-1">
						<ChevronRight aria-hidden className="size-4 shrink-0 text-muted-foreground" />
						{i === path.length - 1 ? (
							<h1 aria-current="page" className="max-w-96 truncate" title={f.name}>
								{f.name}
							</h1>
						) : (
							<a href={hrefOf({ view: "files", folder: f.id })} onClick={link(f)} className={`block ${crumb}`} title={f.name}>
								{f.name}
							</a>
						)}
					</li>
				))}
			</ol>
		</nav>
	);
}
