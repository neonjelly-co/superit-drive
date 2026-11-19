import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Download, Upload, Users } from "lucide-react";
import { type DragEvent, type MouseEvent, useCallback, useEffect, useState } from "react";
import { ApiError, api, type DeletedPlace, type FolderRef, type Item, type Place, type SharedItem, type TrashItem } from "./api";
import { Button } from "./components/button";
import { ConflictDialog } from "./components/conflict-dialog";
import { DetailsPanel, link, whereOf } from "./components/details-panel";
import { ItemList } from "./components/item-list";
import { MoveDialog } from "./components/move-dialog";
import { keyOf, SharedList } from "./components/shared-list";
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

const ROLE_SENTENCE: Record<string, string> = {
	viewer: "You can open and download.",
	editor: "You are an editor: you can also upload, rename, move, delete and share.",
	manager: "You are a manager.",
};

let nextNotice = 1;

/**
 * The Drive as a member sees it: layout D of the prototype. A sidebar on the left, the list in the middle, and the
 * details panel on the right, which opens when one item is selected (or, for the folder being looked at, from "Who can
 * see this"). The list shows a folder of "My files" or of a shared folder (the address says which), or the trash of
 * every place. Files are uploaded into the folder being looked at, with the sidebar's Upload button or by dragging
 * them onto the list. Only what the Drive says the person may do there is offered.
 *
 * What others shared with the person from a place they cannot otherwise open (someone's My files, a shared folder they
 * have no role on as a whole) is in "Shared with me" (`/?shared`). A folder from there opens at its address,
 * `/?place=…&folder=…`: the place is not in the sidebar, and the breadcrumb starts at "Shared with me", then the highest
 * folder they can open. A file there downloads, and its details open beside the list.
 */
export function App() {
	const queryClient = useQueryClient();
	const me = useQuery({ queryKey: ["me"], queryFn: api.me });
	const places = useQuery({ queryKey: ["places"], queryFn: api.places });
	const members = useQuery({ queryKey: ["members"], queryFn: api.members });
	const [where, go] = useWhere();
	const myFiles = places.data?.find((p) => p.kind === "my-files");
	const sharedFolders = places.data?.filter(isShared) ?? [];
	const place =
		where.view !== "files" ? undefined : where.place === null ? myFiles : places.data?.find((p) => p.id === where.place);
	// Part of a place: a folder of a place the person cannot open as a whole, which may have been shared with them.
	const inPart =
		where.view === "files" && where.place !== null && where.folder !== null && places.data !== undefined && !place;
	const placeId = place?.id ?? (inPart && where.view === "files" ? (where.place ?? undefined) : undefined);
	const placeName = place ? nameOfPlace(place) : "My files";
	const folderId = where.view === "files" ? where.folder : null;

	const listing = useQuery({
		queryKey: ["items", placeId, folderId],
		queryFn: () => api.list(placeId as number, folderId),
		enabled: placeId !== undefined && where.view === "files",
		retry: (n, e) => !(e instanceof ApiError && e.status === 404) && n < 3,
	});
	const items = listing.data?.items;
	const path = listing.data?.path ?? [];
	const fromTop = listing.data?.fromTop ?? place !== undefined;
	// What the person may do in this folder: the Drive says, for each folder, as it lists it.
	const can = listing.data?.can ?? [];
	const listingGone = listing.error instanceof ApiError && listing.error.status === 404;
	// A place, or a folder in part of one, that is not there: never was, went to the trash, or the person has no access
	// to it. The Drive answers all of them the same, and so does the page.
	const missingPlace =
		where.view === "files" && where.place !== null && places.data !== undefined && !place && (!inPart || listingGone);
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
	const shared = useQuery({ queryKey: ["shared-with-me"], queryFn: api.sharedWithMe, enabled: where.view === "shared" });
	const [sharedKey, setSharedKey] = useState<string>();
	const sharedSelected = where.view === "shared" ? shared.data?.find((i) => keyOf(i) === sharedKey) : undefined;

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
			setSharedKey(undefined);
			setPlacePanel(false);
			go(w);
		},
		[go],
	);
	const openShared = (item: SharedItem) => goTo({ view: "files", place: item.place, folder: item.id });
	/** The address's way of naming a place: null for My files. */
	const placeParam = (id: number) => (id === myFiles?.id ? null : id);
	const openFolder = (folder: FolderRef | Item | null) =>
		goTo({ view: "files", place: where.view === "files" ? where.place : null, folder: folder?.id ?? null });

	const current = path.at(-1);
	const here = current?.name ?? placeName;
	const project = me.data?.project;
	useEffect(() => {
		const name = where.view === "trash" ? "Trash" : where.view === "shared" ? "Shared with me" : here;
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
	/** Who has access changed: every list of the place says who can see what, and the person's own places may change. */
	const accessChanged = () => {
		void queryClient.invalidateQueries({ queryKey: ["items", placeId] });
		void queryClient.invalidateQueries({ queryKey: ["access", placeId] });
		void queryClient.invalidateQueries({ queryKey: ["places"] });
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

	const folderGone = listingGone;
	const role = listing.data?.role || (place?.kind === "shared-folder" ? place.role : "");
	const note = !place
		? `Shared with you. ${ROLE_SENTENCE[role] ?? ""}`
		: place.kind === "shared-folder"
			? `Owned by the project. ${ROLE_SENTENCE[role] ?? ""} Project admins can open every shared folder.`
			: "Only you can see these, unless you share them. Project admins cannot open them.";
	// The panel for the folder being looked at: the shared folder as a whole at its top, the folder itself further in.
	const herePanel = placePanel && !selected && where.view === "files" && placeId !== undefined;
	const canSeeHere = current ? can.includes("see-access") : place?.kind === "shared-folder";
	// Where an item is, as the person may see it: from the place's name when they can open it, otherwise from "Shared
	// with me", where they found what they were given.
	const rootName = fromTop ? placeName : "Shared with me";
	// The shared folders an item of the person's own My files may be moved into (with the warning).
	const moveInto = sharedFolders.filter((p) => p.can.includes("move-in") && p.id !== placeId).map((p) => ({ id: p.id, name: p.name }));

	return (
		<div className="flex h-screen bg-background text-foreground">
			<Sidebar
				project={project}
				where={where}
				go={goTo}
				sharedFolders={sharedFolders}
				inShared={inPart}
				onUpload={canUpload ? uploads.add : undefined}
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
					droppedFiles(e.dataTransfer).then(uploads.add, (err) =>
						say({ text: `What was dropped could not be read: ${err instanceof Error ? err.message : String(err)}`, failed: true }),
					);
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
					) : where.view === "shared" ? (
						<>
							<h1 className="text-xl font-semibold">Shared with me</h1>
							<p className="mt-1 text-sm text-muted-foreground">
								What others shared with you from their My files, or from shared folders you are not part of. What is in
								a folder is found by opening it.
							</p>
							<SharedList
								items={shared.data}
								loading={shared.isPending}
								error={shared.error}
								onRetry={() => void shared.refetch()}
								onOpen={openShared}
								selected={sharedKey}
								onSelect={(item) => setSharedKey(keyOf(item))}
							/>
							{shared.data && shared.data.length > 0 && (
								<p className="mt-3 text-sm text-muted-foreground">
									Click a name to open it. Click elsewhere on a row to see its details.
								</p>
							)}
						</>
					) : missingPlace ? (
						<div className="mt-16 flex flex-col items-center text-center text-sm text-muted-foreground">
							<p>This is not in the Drive, or you have no access to it.</p>
							<Button className="mt-3" onClick={() => goTo({ view: "files", place: null, folder: null })}>
								Go to My files
							</Button>
						</div>
					) : (
						<>
							<div className="flex items-start gap-3">
								<div className="min-w-0 flex-1">
									<Breadcrumb
										placeName={fromTop ? placeName : undefined}
										place={where.view === "files" ? where.place : null}
										path={path}
										go={goTo}
									/>
								</div>
								{inFiles && placeId !== undefined && can.includes("download") && items && items.length > 0 && (
									<a href={api.zipUrl(placeId, current?.id ?? null)} download className={`${link} h-8 px-2.5`}>
										<Download aria-hidden className="size-4" /> Download as zip
									</a>
								)}
								{inFiles && canSeeHere && (
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
									places.error ? places.refetch() : folderGone ? (fromTop ? openFolder(null) : goTo({ view: "files", place: null, folder: null })) : listing.refetch()
								}
								retryLabel={folderGone ? `Go to ${fromTop ? placeName : "My files"}` : undefined}
								onOpen={openFolder}
								selected={selectedId}
								onSelect={(item) => {
									setPlacePanel(false);
									setSelectedId(item.id);
								}}
								me={me.data?.userId}
								members={members.data}
								canAdd={can.includes("upload")}
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
							<Upload aria-hidden className="size-5 text-muted-foreground" /> Drop files or folders to upload them to {here}
						</p>
					</div>
				)}
				<NoticeBar notice={notice} onClose={closeNotice} />
				<UploadsPanel uploads={uploads.uploads} onCancel={uploads.cancel} onClose={uploads.clear} />
			</div>
			{selected && placeId !== undefined && where.view === "files" && (
				<DetailsPanel
					placeId={placeId}
					where={whereOf(rootName, path)}
					can={selected.can ?? []}
					me={me.data?.userId}
					item={selected}
					busy={busy}
					onClose={() => setSelectedId(undefined)}
					onOpen={openFolder}
					onRename={() => setDialog("rename")}
					onMove={() => setDialog("move")}
					onDelete={() => void remove(selected)}
					onAccessChanged={accessChanged}
				/>
			)}
			{herePanel && current && (
				<DetailsPanel
					key={current.id}
					placeId={placeId}
					where={whereOf(rootName, path.slice(0, -1))}
					can={can}
					me={me.data?.userId}
					item={{ id: current.id, name: current.name, kind: "folder" }}
					here
					onClose={() => setPlacePanel(false)}
					onOpen={openFolder}
					onRename={() => undefined}
					onMove={() => undefined}
					onDelete={() => undefined}
					onAccessChanged={accessChanged}
				/>
			)}
			{sharedSelected && (
				<DetailsPanel
					key={keyOf(sharedSelected)}
					placeId={sharedSelected.place}
					where="Shared with me"
					can={sharedSelected.can ?? []}
					me={me.data?.userId}
					item={sharedSelected}
					onClose={() => setSharedKey(undefined)}
					onOpen={() => openShared(sharedSelected)}
					onRename={() => undefined}
					onMove={() => undefined}
					onDelete={() => undefined}
					onAccessChanged={() => {
						void queryClient.invalidateQueries({ queryKey: ["shared-with-me"] });
						void queryClient.invalidateQueries({ queryKey: ["items", sharedSelected.place] });
					}}
				/>
			)}
			{herePanel && !current && place?.kind === "shared-folder" && (
				<SharedFolderPanel
					key={place.id}
					place={place}
					me={me.data?.userId}
					onClose={() => setPlacePanel(false)}
					onDelete={() => deleteSharedFolder(place)}
					onAccessChanged={accessChanged}
				/>
			)}

			{dialog === "new-folder" && placeId !== undefined && (
				<NameDialog
					title="New folder"
					description={`In ${whereOf(rootName, path)}`}
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
					place={{ id: placeId, name: placeName }}
					root={fromTop ? null : (path[0] ?? null)}
					item={selected}
					path={path}
					otherPlaces={selected.can?.includes("move-out") ? moveInto : []}
					me={me.data?.userId}
					members={members.data}
					onMove={async ({ place: to, folder }) => {
						const there = folder?.name ?? to.name;
						if (to.id === placeId) {
							await api.move(placeId, selected.id, folder?.id ?? null);
							setSelectedId(undefined);
							refresh(folderId);
							refresh(folder?.id ?? null);
							say({ text: `“${selected.name}” was moved to ${there}.`, action: { label: "Show", run: () => openFolder(folder) } });
							return;
						}
						await api.move(placeId, selected.id, folder?.id ?? null, to.id);
						setSelectedId(undefined);
						refresh(folderId);
						void queryClient.invalidateQueries({ queryKey: ["items", to.id] });
						void queryClient.invalidateQueries({ queryKey: ["access"] });
						say({
							text: `“${selected.name}” was moved to ${there}. It belongs to the project now.`,
							action: { label: "Show", run: () => goTo({ view: "files", place: to.id, folder: folder?.id ?? null }) },
						});
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{uploads.question && <ConflictDialog key={uploads.question.key} question={uploads.question} onAnswer={uploads.answer} />}
		</div>
	);
}

/**
 * Where the list is: the place and the folders down to the one shown, each but the last a link up to it. For someone
 * given part of a place (placeName undefined), it starts at the highest folder they can open.
 */
function Breadcrumb({
	placeName,
	place,
	path,
	go,
}: { placeName?: string; place: number | null; path: FolderRef[]; go: (w: Where) => void }) {
	const link = (to: FolderRef | null) => (e: MouseEvent) => {
		if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
		e.preventDefault();
		go({ view: "files", place, folder: to?.id ?? null });
	};
	if (path.length === 0) return <h1 className="text-xl font-semibold break-words">{placeName ?? ""}</h1>;
	const crumb = "max-w-64 truncate underline-offset-4 hover:underline";
	const toShared = (e: MouseEvent) => {
		if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
		e.preventDefault();
		go({ view: "shared" });
	};
	return (
		<nav aria-label="Breadcrumb">
			<ol className="flex min-w-0 flex-wrap items-center gap-1 text-xl font-semibold">
				<li>
					{placeName !== undefined ? (
						<a href={hrefOf({ view: "files", place, folder: null })} onClick={link(null)} className={`block ${crumb}`} title={placeName}>
							{placeName}
						</a>
					) : (
						// Given part of a place: it was found in "Shared with me", and nothing above it is theirs to see.
						<a href={hrefOf({ view: "shared" })} onClick={toShared} className={`block ${crumb}`}>
							Shared with me
						</a>
					)}
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
