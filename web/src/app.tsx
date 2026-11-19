import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Upload, Users } from "lucide-react";
import { type DragEvent, type MouseEvent, useCallback, useEffect, useState } from "react";
import { ApiError, api, type DeletedPlace, type FolderRef, type Item, type Place, type TrashItem } from "./api";
import { Button } from "./components/button";
import { ConflictDialog } from "./components/conflict-dialog";
import { DetailsPanel } from "./components/details-panel";
import { ItemList } from "./components/item-list";
import { MoveDialog } from "./components/move-dialog";
import { NameDialog } from "./components/name-dialog";
import { type Notice, NoticeBar } from "./components/notice";
import { SharedFolderPanel } from "./components/shared-folder-panel";
import { Sidebar } from "./components/sidebar";
import { TrashList, type TrashRow } from "./components/trash-list";
import { UploadsPanel } from "./components/uploads-panel";
import { hrefOf, useWhere, type Where } from "./location";
import { freeName } from "./names";
import { droppedFiles, useUploads } from "./uploads";

const carriesFiles = (e: { dataTransfer: DataTransfer | null }) => e.dataTransfer?.types.includes("Files") ?? false;

type SharedFolder = Extract<Place, { kind: "shared-folder" }>;
const isShared = (p: Place): p is SharedFolder => p.kind === "shared-folder";

/** A place's name as people see it. */
const nameOfPlace = (p: Place) => (p.kind === "my-files" ? "My files" : p.name);

const ROLE_SENTENCE = { viewer: "You can open and download.", editor: "You are an editor.", manager: "You are a manager." };

let nextNotice = 1;

/**
 * The Drive as a member sees it: layout D of the prototype. A sidebar on the left, the list in the middle, and the
 * details panel on the right, which opens when one item is selected (or, for a shared folder itself, from "Who can see
 * this"). The list shows a folder of "My files" or of a shared folder (the address says which), or the trash of every
 * place. Files are uploaded into the folder being looked at, with the sidebar's Upload button or by dragging them onto
 * the list. Only what the Drive says the person may do in the place is offered.
 */
export function App() {
	const queryClient = useQueryClient();
	const me = useQuery({ queryKey: ["me"], queryFn: api.me });
	const places = useQuery({ queryKey: ["places"], queryFn: api.places });
	const [where, go] = useWhere();
	const myFiles = places.data?.find((p) => p.kind === "my-files");
	const sharedFolders = places.data?.filter(isShared) ?? [];
	const place =
		where.view !== "files" ? undefined : where.place === null ? myFiles : places.data?.find((p) => p.id === where.place);
	const placeId = place?.id;
	const placeName = place ? nameOfPlace(place) : "My files";
	const can = place?.can ?? [];
	// A shared folder that is not there: never was, went to the trash, or the person has no role on it. The Drive
	// answers all three the same, and so does the page.
	const missingPlace = where.view === "files" && where.place !== null && places.data !== undefined && !place;
	const folderId = where.view === "files" ? where.folder : null;

	const listing = useQuery({
		queryKey: ["items", placeId, folderId],
		queryFn: () => api.list(placeId as number, folderId),
		enabled: placeId !== undefined && where.view === "files",
	});
	const items = listing.data?.items;
	const path = listing.data?.path ?? [];
	const inFiles = where.view === "files" && placeId !== undefined && listing.data !== undefined;
	const uploads = useUploads(placeId, folderId, items);

	// The trash of every place the person may restore from, and the shared folders in the trash they may restore.
	const trashPlaces = where.view === "trash" ? (places.data?.filter((p) => p.can.includes("see-trash")) ?? []) : [];
	const trashes = useQueries({
		queries: trashPlaces.map((p) => ({ queryKey: ["trash", p.id], queryFn: () => api.trash(p.id) })),
	});
	const deletedPlaces = useQuery({
		queryKey: ["deleted-shared-folders"],
		queryFn: api.deletedSharedFolders,
		enabled: where.view === "trash",
	});

	const [selectedId, setSelectedId] = useState<number>();
	const selected = items?.find((i) => i.id === selectedId);
	const [placePanel, setPlacePanel] = useState(false);
	const [dialog, setDialog] = useState<"new-folder" | "new-shared-folder" | "rename" | "move" | null>(null);
	const [busy, setBusy] = useState(false);
	const [restoring, setRestoring] = useState<string>();
	const [notice, setNotice] = useState<Notice | null>(null);
	const [dragging, setDragging] = useState(false);
	const say = useCallback((n: Omit<Notice, "key">) => setNotice({ ...n, key: nextNotice++ }), []);
	const closeNotice = useCallback(() => setNotice(null), []);

	const goTo = useCallback(
		(w: Where) => {
			setSelectedId(undefined);
			setPlacePanel(false);
			go(w);
		},
		[go],
	);
	/** The address's way of naming a place: null for My files. */
	const placeParam = (id: number) => (id === myFiles?.id ? null : id);
	const openFolder = (folder: FolderRef | Item | null) =>
		goTo({ view: "files", place: where.view === "files" ? where.place : null, folder: folder?.id ?? null });

	const here = path.at(-1)?.name ?? placeName;
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

	const canUpload = inFiles && can.includes("upload");
	const dragOver = (e: DragEvent) => {
		if (!carriesFiles(e) || !canUpload) return;
		e.preventDefault();
		e.dataTransfer.dropEffect = "copy";
		setDragging(true);
	};

	/** What changed in a folder of the place being looked at: its list, and the space used. */
	const refresh = (folder: number | null) => {
		void queryClient.invalidateQueries({ queryKey: ["items", placeId, folder] });
		void queryClient.invalidateQueries({ queryKey: ["usage"] });
	};

	const remove = async (item: Item) => {
		if (placeId === undefined) return;
		const from = { id: placeId, name: placeName };
		setBusy(true);
		try {
			const t = await api.remove(placeId, item.id);
			setSelectedId(undefined);
			refresh(folderId);
			void queryClient.invalidateQueries({ queryKey: ["trash", placeId] });
			say({ text: `“${t.name}” was moved to the trash.`, action: { label: "Undo", run: () => void restore(from, t) } });
		} catch (e) {
			say({ text: e instanceof Error ? e.message : String(e), failed: true });
			refresh(folderId);
		} finally {
			setBusy(false);
		}
	};

	const restore = async (from: { id: number; name: string }, t: TrashItem) => {
		setRestoring(`${from.id}:${t.id}`);
		try {
			const r = await api.restore(from.id, t.id);
			const to = r.path.at(-1) ?? null;
			void queryClient.invalidateQueries({ queryKey: ["items", from.id, to?.id ?? null] });
			void queryClient.invalidateQueries({ queryKey: ["usage"] });
			void queryClient.invalidateQueries({ queryKey: ["trash", from.id] });
			const back = [from.name, ...r.path.map((f) => f.name)].join(" › ");
			const renamed = r.item.name !== t.name ? ` as “${r.item.name}”, as “${t.name}” is taken there` : "";
			say({
				text: `“${t.name}” is back in ${back}${renamed}.`,
				action: { label: "Show", run: () => goTo({ view: "files", place: placeParam(from.id), folder: to?.id ?? null }) },
			});
		} catch (e) {
			say({ text: e instanceof Error ? e.message : String(e), failed: true });
			void queryClient.invalidateQueries({ queryKey: ["trash", from.id] });
		} finally {
			setRestoring(undefined);
		}
	};

	const restoreSharedFolder = async (d: Pick<DeletedPlace, "id" | "name">) => {
		setRestoring(`place:${d.id}`);
		try {
			await api.restoreSharedFolder(d.id);
			void queryClient.invalidateQueries({ queryKey: ["places"] });
			void queryClient.invalidateQueries({ queryKey: ["deleted-shared-folders"] });
			say({
				text: `“${d.name}” is back, with everything in it.`,
				action: { label: "Show", run: () => goTo({ view: "files", place: d.id, folder: null }) },
			});
		} catch (e) {
			say({ text: e instanceof Error ? e.message : String(e), failed: true });
			void queryClient.invalidateQueries({ queryKey: ["deleted-shared-folders"] });
		} finally {
			setRestoring(undefined);
		}
	};

	const deleteSharedFolder = async (p: SharedFolder) => {
		const d = await api.deleteSharedFolder(p.id);
		goTo({ view: "files", place: null, folder: null });
		void queryClient.invalidateQueries({ queryKey: ["places"] });
		void queryClient.invalidateQueries({ queryKey: ["deleted-shared-folders"] });
		say({ text: `“${d.name}” was moved to the trash.`, action: { label: "Undo", run: () => void restoreSharedFolder(d) } });
	};

	// Everything in the trash, the most recently deleted first.
	const trashRows: { row: TrashRow; restore: () => void }[] = [];
	trashPlaces.forEach((p, i) => {
		const from = { id: p.id, name: nameOfPlace(p) };
		for (const t of trashes[i]?.data ?? []) {
			trashRows.push({
				row: { ...t, key: `${p.id}:${t.id}`, backTo: [from.name, ...t.from.map((f) => f.name)].join(" › ") },
				restore: () => void restore(from, t),
			});
		}
	});
	for (const d of deletedPlaces.data ?? []) {
		trashRows.push({ row: { ...d, key: `place:${d.id}`, backTo: "Shared folders" }, restore: () => void restoreSharedFolder(d) });
	}
	trashRows.sort((a, b) => b.row.deleted.localeCompare(a.row.deleted));
	const trashError = places.error ?? deletedPlaces.error ?? trashes.find((q) => q.error)?.error;
	const trashLoading = places.isPending || deletedPlaces.isPending || trashes.some((q) => q.isPending);

	const folderGone = listing.error instanceof ApiError && listing.error.status === 404;
	const note =
		place?.kind === "shared-folder"
			? `Owned by the project. ${ROLE_SENTENCE[place.role]} Project admins can open every shared folder.`
			: "Only you can see these, unless you share them. Project admins cannot open them.";

	return (
		<div className="flex h-screen bg-background text-foreground">
			<Sidebar
				project={project}
				where={where}
				go={goTo}
				sharedFolders={sharedFolders}
				onUpload={canUpload ? (files) => uploads.add(files.map((file) => ({ file }))) : undefined}
				onNewFolder={inFiles && can.includes("make-folder") ? () => setDialog("new-folder") : undefined}
				onNewSharedFolder={() => setDialog("new-shared-folder")}
			/>
			<div
				className="relative min-w-0 flex-1"
				onDragEnter={dragOver}
				onDragOver={dragOver}
				onDragLeave={(e) => {
					if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
				}}
				onDrop={(e) => {
					if (!carriesFiles(e) || !canUpload) return;
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
								rows={trashRows.map((r) => r.row)}
								loading={trashLoading}
								error={trashError}
								onRetry={() => {
									void places.refetch();
									void deletedPlaces.refetch();
									for (const q of trashes) void q.refetch();
								}}
								onRestore={(row) => trashRows.find((r) => r.row.key === row.key)?.restore()}
								restoring={restoring}
							/>
						</>
					) : missingPlace ? (
						<div className="mt-16 flex flex-col items-center text-center text-sm text-muted-foreground">
							<p>This shared folder is not there, or you have no access to it.</p>
							<Button className="mt-3" onClick={() => goTo({ view: "files", place: null, folder: null })}>
								Go to My files
							</Button>
						</div>
					) : (
						<>
							<div className="flex items-start gap-3">
								<div className="min-w-0 flex-1">
									<Breadcrumb placeName={placeName} place={where.view === "files" ? where.place : null} path={path} go={goTo} />
								</div>
								{place?.kind === "shared-folder" && (
									<Button
										small
										onClick={() => {
											setSelectedId(undefined);
											setPlacePanel(true);
										}}
									>
										<Users aria-hidden /> Who can see this
									</Button>
								)}
							</div>
							<p className="mt-1 text-sm text-muted-foreground">{note}</p>
							<ItemList
								placeId={placeId}
								items={items}
								loading={places.isPending || listing.isPending}
								error={places.error ?? listing.error}
								onRetry={() =>
									places.error ? places.refetch() : folderGone ? openFolder(null) : listing.refetch()
								}
								retryLabel={folderGone ? `Go to ${placeName}` : undefined}
								onOpen={openFolder}
								selected={selectedId}
								onSelect={(item) => {
									setPlacePanel(false);
									setSelectedId(item.id);
								}}
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
					placeName={placeName}
					can={can}
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
			{!selected && placePanel && place?.kind === "shared-folder" && where.view === "files" && (
				<SharedFolderPanel
					key={place.id}
					place={place}
					onClose={() => setPlacePanel(false)}
					onDelete={() => deleteSharedFolder(place)}
				/>
			)}

			{dialog === "new-folder" && placeId !== undefined && (
				<NameDialog
					title="New folder"
					description={`In ${[placeName, ...path.map((f) => f.name)].join(" › ")}`}
					initial={freeName("New folder", items, true)}
					submitLabel="Make folder"
					onSubmit={async (name) => {
						await api.newFolder(placeId, folderId, name);
						refresh(folderId);
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{dialog === "new-shared-folder" && (
				<NameDialog
					title="New shared folder"
					description="A shared folder belongs to the project, not to you. You become its manager and decide who has access to it. Project admins can open every shared folder."
					initial=""
					submitLabel="Make shared folder"
					onSubmit={async (name) => {
						const p = await api.newSharedFolder(name);
						await queryClient.invalidateQueries({ queryKey: ["places"] });
						goTo({ view: "files", place: p.id, folder: null });
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
					placeName={placeName}
					item={selected}
					path={path}
					onMove={async (to) => {
						await api.move(placeId, selected.id, to?.id ?? null);
						setSelectedId(undefined);
						refresh(folderId);
						refresh(to?.id ?? null);
						say({ text: `“${selected.name}” was moved to ${to?.name ?? placeName}.`, action: { label: "Show", run: () => openFolder(to) } });
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{uploads.question && <ConflictDialog key={uploads.question.key} question={uploads.question} onAnswer={uploads.answer} />}
		</div>
	);
}

/** Where the list is: the place and the folders down to the one shown, each but the last a link up to it. */
function Breadcrumb({
	placeName,
	place,
	path,
	go,
}: { placeName: string; place: number | null; path: FolderRef[]; go: (w: Where) => void }) {
	const link = (to: FolderRef | null) => (e: MouseEvent) => {
		if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
		e.preventDefault();
		go({ view: "files", place, folder: to?.id ?? null });
	};
	if (path.length === 0) return <h1 className="text-xl font-semibold break-words">{placeName}</h1>;
	const crumb = "max-w-64 truncate underline-offset-4 hover:underline";
	return (
		<nav aria-label="Breadcrumb">
			<ol className="flex min-w-0 flex-wrap items-center gap-1 text-xl font-semibold">
				<li>
					<a href={hrefOf({ view: "files", place, folder: null })} onClick={link(null)} className={`block ${crumb}`} title={placeName}>
						{placeName}
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
							<a href={hrefOf({ view: "files", place, folder: f.id })} onClick={link(f)} className={`block ${crumb}`} title={f.name}>
								{f.name}
							</a>
						)}
					</li>
				))}
			</ol>
		</nav>
	);
}
