import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckSquare, ChevronRight, Download, FolderInput, Menu, Trash2, Upload, Users, X } from "lucide-react";
import { type DragEvent, type MouseEvent, useCallback, useEffect, useRef, useState } from "react";
import {
	ApiError,
	api,
	type DeletedPlace,
	type FolderRef,
	type Found,
	type Item,
	type Listing,
	type Place,
	type SharedItem,
	type TrashItem,
} from "./api";
import { Button } from "./components/button";
import { type BulkOutcome, BulkResultDialog, eachOf, type Failure } from "./components/bulk-result";
import { ConflictDialog } from "./components/conflict-dialog";
import { DetailsPanel, link, whereOf } from "./components/details-panel";
import { ItemList } from "./components/item-list";
import { MoveDialog } from "./components/move-dialog";
import { NameDialog } from "./components/name-dialog";
import { type Notice, NoticeBar } from "./components/notice";
import { OversightView } from "./components/oversight-view";
import { RollbackNotice } from "./components/rollback-notice";
import { SearchField } from "./components/search-field";
import { keyOfFound, locationOf, SearchList } from "./components/search-list";
import { SharedFolderPanel } from "./components/shared-folder-panel";
import { keyOf, SharedList } from "./components/shared-list";
import { Sidebar } from "./components/sidebar";
import { TrashList, type TrashRow } from "./components/trash-list";
import { UploadsPanel } from "./components/uploads-panel";
import { Wordmark } from "./components/wordmark";
import { t } from "./i18n";
import { hrefOf, useWhere, type Where } from "./location";
import { freeName } from "./names";
import { useNarrow } from "./narrow";
import { droppedFiles, useUploads } from "./uploads";

const carriesFiles = (e: { dataTransfer: DataTransfer | null }) => e.dataTransfer?.types.includes("Files") ?? false;

type SharedFolder = Extract<Place, { kind: "shared-folder" }>;
const isShared = (p: Place): p is SharedFolder => p.kind === "shared-folder";

/** A place's name as people see it. */
const nameOfPlace = (p: Place) => (p.kind === "my-files" ? t.app.myFiles : p.name);

/** What the person may do in a folder, by their role there, in one sentence. */
const roleSentence = (role: string) =>
	role === "viewer" ? t.note.viewer : role === "editor" ? t.note.editor : role === "manager" ? t.note.manager : "";

let nextNotice = 1;

/** Whether two lists of a folder show the same: the same items, as they are, and the same path and rights. */
const sameList = (a: Listing, b: Listing) =>
	JSON.stringify({ ...a, checking: false }) === JSON.stringify({ ...b, checking: false });

/** What the bulk result dialog says: a change asked of several items that did not happen to all of them. */
type BulkResult = { title: string; done: string; stayed: string; failed: Failure[]; undo?: { label: string; run: () => void } };

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
 * folder they can open. A file there opens or downloads, and its details open beside the list.
 *
 * Above the list, a search finds files and folders by name across everything the person can see (`/?search=…`). In a
 * folder's list, tick boxes select several items; with two or more selected, the list's header becomes a bar to move,
 * download as one zip or delete them together. Each is changed by its own request, and when some are not, a dialog says
 * which and why.
 *
 * On a phone the same page folds: the sidebar is behind a menu button, each row is a name with one line under it, the
 * details are a sheet from the bottom, and tick boxes show only after "Select" or a press and hold on a row, with what
 * can be done with the items selected in a bar at the bottom.
 */
export function App() {
	const queryClient = useQueryClient();
	const narrow = useNarrow();
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
	const placeName = place ? nameOfPlace(place) : t.app.myFiles;
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

	// The list comes from the Drive's index at once, while the Drive looks at the folder on the storage (`checking`):
	// one more request waits for that look, and its list replaces this one. While the person is acting on the list (an
	// item selected, a dialog open, a change under way) it waits, so nothing moves under their hand: a line above the
	// list says it changed, and it is shown when they ask or are done.
	const [changedList, setChangedList] = useState<{ key: string; listing: Listing }>();
	const listKey = `${placeId}:${folderId}`;
	const checking = listing.data?.checking === true && placeId !== undefined;
	useEffect(() => {
		if (!checking || placeId === undefined) return;
		let stop = false;
		(async () => {
			for (let i = 0; i < 6 && !stop; i++) {
				let next: Listing;
				try {
					next = await api.list(placeId, folderId, true);
				} catch (e) {
					// Gone from the storage, or the person's access with it: the list says so.
					if (!stop && e instanceof ApiError && e.status === 404) void queryClient.invalidateQueries({ queryKey: ["items", placeId, folderId] });
					return;
				}
				if (stop) return;
				if (!next.checking || i === 5) {
					setChangedList({ key: `${placeId}:${folderId}`, listing: { ...next, checking: false } });
					return;
				}
			}
		})();
		return () => {
			stop = true;
		};
	}, [checking, placeId, folderId, listing.dataUpdatedAt, queryClient]);

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
	const query = where.view === "search" ? where.query : "";
	const found = useQuery({
		queryKey: ["search", query],
		queryFn: () => api.search(query),
		enabled: where.view === "search",
		retry: (n, e) => !(e instanceof ApiError && e.status === 400) && n < 1,
	});
	const [foundKey, setFoundKey] = useState<string>();
	const foundSelected = where.view === "search" ? found.data?.items.find((f) => keyOfFound(f) === foundKey) : undefined;
	// Where the person was before they started searching: emptying the search goes back there.
	const beforeSearch = useRef<Where>({ view: "files", place: null, folder: null });

	// The items selected in the list, by id: one shows its details; two or more, the bulk bar. Only those still in the
	// list count. On a phone, ticking is a mode of its own (selecting): the details of one item ticked are not shown
	// then, and the bar is at the bottom.
	const [ticked, setTicked] = useState<ReadonlySet<number>>(new Set());
	const [selecting, setSelecting] = useState(false);
	const choosing = narrow && selecting;
	const tickedItems = items?.filter((i) => ticked.has(i.id)) ?? [];
	const selected = tickedItems.length === 1 && !choosing ? tickedItems[0] : undefined;
	const [placePanel, setPlacePanel] = useState(false);
	const [menuOpen, setMenuOpen] = useState(false);
	const [dialog, setDialog] = useState<"new-folder" | "new-shared-folder" | "rename" | null>(null);
	// What the Move dialog moves: the selected item, or every item selected.
	const [moving, setMoving] = useState<Item[]>();
	const [busy, setBusy] = useState(false);
	// While a change to several items is under way: how far it has got.
	const [progress, setProgress] = useState<string>();
	const [bulkResult, setBulkResult] = useState<BulkResult>();
	const [restoring, setRestoring] = useState<string>();
	const [notice, setNotice] = useState<Notice | null>(null);
	// The list the look at the storage brought, while it waits to be shown; whether it differs from the one shown.
	const waiting = changedList?.key === listKey ? changedList.listing : undefined;
	const differs = waiting !== undefined && listing.data !== undefined && !sameList(waiting, listing.data);
	const acting =
		ticked.size > 0 || choosing || dialog !== null || moving !== undefined || busy || progress !== undefined || bulkResult !== undefined || uploads.question !== undefined;
	const showChanges = useCallback(() => {
		if (!waiting || placeId === undefined) return;
		queryClient.setQueryData(["items", placeId, folderId], waiting);
		setChangedList(undefined);
	}, [waiting, placeId, folderId, queryClient]);
	useEffect(() => {
		if (waiting && (!differs || !acting)) showChanges();
	}, [waiting, differs, acting, showChanges]);
	const [dragging, setDragging] = useState(false);
	const say = useCallback((n: Omit<Notice, "key">) => setNotice({ ...n, key: nextNotice++ }), []);
	const closeNotice = useCallback(() => setNotice(null), []);
	const closeMenu = useCallback(() => setMenuOpen(false), []);
	const clearSelection = useCallback(() => {
		setTicked(new Set());
		setSelecting(false);
	}, []);

	const goTo = useCallback(
		(w: Where, how?: { replace?: boolean }) => {
			setTicked(new Set());
			setSelecting(false);
			setSharedKey(undefined);
			setFoundKey(undefined);
			setPlacePanel(false);
			setMenuOpen(false);
			go(w, how);
		},
		[go],
	);
	const onSearch = useCallback(
		(text: string) => {
			if (where.view !== "search") beforeSearch.current = where;
			// The first search is a new step in the browser's history; what is typed after it changes that step.
			goTo({ view: "search", query: text.trim() }, { replace: where.view === "search" });
		},
		[where, goTo],
	);
	const endSearch = useCallback(() => {
		if (where.view === "search") goTo(beforeSearch.current);
	}, [where.view, goTo]);
	const openShared = (item: SharedItem) => goTo({ view: "files", place: item.place, folder: item.id });
	/** The address's way of naming a place: null for My files. */
	const placeParam = (id: number) => (id === myFiles?.id ? null : id);
	const openFolder = (folder: FolderRef | Item | null) =>
		goTo({ view: "files", place: where.view === "files" ? where.place : null, folder: folder?.id ?? null });
	/** A folder a search found, opened at its own address. */
	const openFound = (f: Found) => goTo({ view: "files", place: placeParam(f.place), folder: f.id });
	/** The folder a search result is in; "Shared with me" for one shared on its own. */
	const showFound = (f: Found) =>
		!f.fromTop && f.path.length === 0
			? goTo({ view: "shared" })
			: goTo({ view: "files", place: placeParam(f.place), folder: f.path.at(-1)?.id ?? null });
	const select = (item: Item) => {
		setPlacePanel(false);
		setTicked(new Set([item.id]));
	};
	const tick = (item: Item, on: boolean) => {
		setPlacePanel(false);
		setTicked((t) => {
			const next = new Set(t);
			if (on) next.add(item.id);
			else next.delete(item.id);
			return next;
		});
	};
	const tickAll = (on: boolean) => {
		setPlacePanel(false);
		setTicked(on ? new Set(items?.map((i) => i.id)) : new Set());
	};
	/** On a phone: "Select", or a press and hold on a row (which ticks it), starts choosing several. */
	const startSelecting = (item?: Item) => {
		setPlacePanel(false);
		setSelecting(true);
		setTicked(item ? new Set([item.id]) : new Set());
	};

	const current = path.at(-1);
	const here = current?.name ?? placeName;
	const project = me.data?.project;
	useEffect(() => {
		const name =
			where.view === "trash"
				? t.app.trash
				: where.view === "shared"
					? t.app.sharedWithMe
					: where.view === "search"
						? t.app.search
						: where.view === "oversight"
							? t.app.sharingAndSpace
							: here;
		document.title = project ? `${name} · ${project}` : name;
	}, [where.view, here, project]);

	// Escape clears the selection, unless something else (a dialog, a field) takes it.
	const anyTicked = tickedItems.length > 0 || choosing;
	useEffect(() => {
		if (!anyTicked) return;
		const clear = (e: KeyboardEvent) => {
			const target = e.target as HTMLElement | null;
			if (e.key !== "Escape" || e.defaultPrevented || document.querySelector("dialog[open]")) return;
			if (target?.closest("input:not([type=checkbox]), textarea, select")) return;
			clearSelection();
		};
		window.addEventListener("keydown", clear);
		return () => window.removeEventListener("keydown", clear);
	}, [anyTicked, clearSelection]);

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
			const r = await api.remove(placeId, item.id);
			setTicked(new Set());
			refresh(folderId);
			void queryClient.invalidateQueries({ queryKey: ["trash", placeId] });
			say({ text: t.notices.toTrash(r.name), action: { label: t.common.undo, run: () => void restore(from, r) } });
		} catch (e) {
			say({ text: e instanceof Error ? e.message : String(e), failed: true });
			refresh(folderId);
		} finally {
			setBusy(false);
		}
	};

	const restore = async (from: { id: number; name: string }, item: TrashItem) => {
		setRestoring(`${from.id}:${item.id}`);
		try {
			const r = await api.restore(from.id, item.id);
			const to = r.path.at(-1) ?? null;
			void queryClient.invalidateQueries({ queryKey: ["items", from.id, to?.id ?? null] });
			void queryClient.invalidateQueries({ queryKey: ["usage"] });
			void queryClient.invalidateQueries({ queryKey: ["trash", from.id] });
			const back = [from.name, ...r.path.map((f) => f.name)].join(" › ");
			say({
				text: r.item.name !== item.name ? t.notices.backInAs(item.name, back, r.item.name) : t.notices.backIn(item.name, back),
				action: { label: t.common.show, run: () => goTo({ view: "files", place: placeParam(from.id), folder: to?.id ?? null }) },
			});
		} catch (e) {
			say({ text: e instanceof Error ? e.message : String(e), failed: true });
			void queryClient.invalidateQueries({ queryKey: ["trash", from.id] });
		} finally {
			setRestoring(undefined);
		}
	};

	/** Deletes every item selected, one request each, and says what happened: Undo, or which were not deleted and why. */
	const removeSeveral = async (list: Item[]) => {
		if (placeId === undefined || list.length === 0) return;
		const from = { id: placeId, name: placeName };
		setProgress(t.progress.toTrash(0, list.length));
		const r = await eachOf(list, (it) => api.remove(from.id, it.id), (n) => setProgress(t.progress.toTrash(n, list.length)));
		setProgress(undefined);
		clearSelection();
		refresh(folderId);
		void queryClient.invalidateQueries({ queryKey: ["trash", from.id] });
		const undo = { label: t.common.undo, run: () => void restoreSeveral(from, r.done) };
		if (r.failed.length === 0) {
			say({ text: t.notices.manyToTrash(r.done.length), action: undo });
			return;
		}
		setBulkResult({
			title: t.bulk.notDeleted(r.failed.length, list.length),
			done: r.done.length > 0 ? t.notices.manyToTrash(r.done.length) : t.bulk.noneDeleted,
			stayed: r.failed.length === 1 ? t.bulk.deletedStayedOne : t.bulk.deletedStayedMany,
			failed: r.failed,
			undo: r.done.length > 0 ? { label: t.bulk.undoDelete, run: undo.run } : undefined,
		});
	};

	/** Brings several items back from a place's trash (Undo after deleting several), and says what happened. */
	const restoreSeveral = async (from: { id: number; name: string }, list: TrashItem[]) => {
		setProgress(t.progress.restoring(0, list.length));
		const r = await eachOf(list, (it) => api.restore(from.id, it.id), (n) => setProgress(t.progress.restoring(n, list.length)));
		setProgress(undefined);
		void queryClient.invalidateQueries({ queryKey: ["items", from.id] });
		void queryClient.invalidateQueries({ queryKey: ["usage"] });
		void queryClient.invalidateQueries({ queryKey: ["trash", from.id] });
		if (r.failed.length === 0) {
			say({ text: t.notices.manyBack(r.done.length) });
			return;
		}
		setBulkResult({
			title: t.bulk.notRestored(r.failed.length, list.length),
			done: r.done.length > 0 ? t.notices.manyBack(r.done.length) : t.bulk.noneRestored,
			stayed: r.failed.length === 1 ? t.bulk.restoreStayedOne : t.bulk.restoreStayedMany,
			failed: r.failed,
		});
	};

	const restoreSharedFolder = async (d: Pick<DeletedPlace, "id" | "name">) => {
		setRestoring(`place:${d.id}`);
		try {
			await api.restoreSharedFolder(d.id);
			void queryClient.invalidateQueries({ queryKey: ["places"] });
			void queryClient.invalidateQueries({ queryKey: ["deleted-shared-folders"] });
			say({
				text: t.notices.sharedFolderBack(d.name),
				action: { label: t.common.show, run: () => goTo({ view: "files", place: d.id, folder: null }) },
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
		say({ text: t.notices.toTrash(d.name), action: { label: t.common.undo, run: () => void restoreSharedFolder(d) } });
	};

	// Everything in the trash, the most recently deleted first.
	const trashRows: { row: TrashRow; restore: () => void }[] = [];
	trashPlaces.forEach((p, i) => {
		const from = { id: p.id, name: nameOfPlace(p) };
		for (const it of trashes[i]?.data ?? []) {
			trashRows.push({
				row: { ...it, key: `${p.id}:${it.id}`, backTo: [from.name, ...it.from.map((f) => f.name)].join(" › ") },
				restore: () => void restore(from, it),
			});
		}
	});
	for (const d of deletedPlaces.data ?? []) {
		trashRows.push({ row: { ...d, key: `place:${d.id}`, backTo: t.common.sharedFolders }, restore: () => void restoreSharedFolder(d) });
	}
	trashRows.sort((a, b) => b.row.deleted.localeCompare(a.row.deleted));
	const trashError = places.error ?? deletedPlaces.error ?? trashes.find((q) => q.error)?.error;
	const trashLoading = places.isPending || deletedPlaces.isPending || trashes.some((q) => q.isPending);

	const folderGone = listingGone;
	const role = listing.data?.role || (place?.kind === "shared-folder" ? place.role : "");
	const note = !place
		? [t.note.sharedWithYou, roleSentence(role)].filter(Boolean).join(" ")
		: place.kind === "shared-folder"
			? [t.note.ownedByProject, roleSentence(role), t.note.adminsOpenShared].filter(Boolean).join(" ")
			: t.note.myFiles;
	// The panel for the folder being looked at: the shared folder as a whole at its top, the folder itself further in.
	const herePanel = placePanel && !selected && where.view === "files" && placeId !== undefined;
	const canSeeHere = current ? can.includes("see-access") : place?.kind === "shared-folder";
	// Where an item is, as the person may see it: from the place's name when they can open it, otherwise from "Shared
	// with me", where they found what they were given.
	const rootName = fromTop ? placeName : t.app.sharedWithMe;
	// The shared folders an item of the person's own My files may be moved into (with the warning).
	const moveInto = sharedFolders.filter((p) => p.can.includes("move-in") && p.id !== placeId).map((p) => ({ id: p.id, name: p.name }));

	/**
	 * Moves what the Move dialog moves into a folder of this place or, from the person's own My files, of a shared
	 * folder: one request per item. A single item that cannot move keeps the dialog open with the reason; of several,
	 * those that move do, and a dialog then says which did not, and why.
	 */
	const moveTo = async (list: Item[], to: { id: number; name: string }, folder: FolderRef | null) => {
		if (placeId === undefined) return;
		const there = folder?.name ?? to.name;
		const intoPlace = to.id === placeId ? undefined : to.id;
		const show = {
			label: t.common.show,
			run: () => (intoPlace === undefined ? openFolder(folder) : goTo({ view: "files", place: to.id, folder: folder?.id ?? null })),
		};
		const changed = () => {
			clearSelection();
			refresh(folderId);
			if (intoPlace === undefined) refresh(folder?.id ?? null);
			else {
				void queryClient.invalidateQueries({ queryKey: ["items", to.id] });
				void queryClient.invalidateQueries({ queryKey: ["access"] });
			}
		};
		const movedMany = (n: number) => (intoPlace === undefined ? t.notices.movedMany(n, there) : t.notices.movedManyToProject(n, there));
		const only = list.length === 1 ? list[0] : undefined;
		if (only) {
			await api.move(placeId, only.id, folder?.id ?? null, intoPlace);
			changed();
			say({
				text: intoPlace === undefined ? t.notices.movedOne(only.name, there) : t.notices.movedOneToProject(only.name, there),
				action: show,
			});
			return;
		}
		setProgress(t.progress.moving(0, list.length));
		const r: BulkOutcome<unknown> = await eachOf(
			list,
			(it) => api.move(placeId, it.id, folder?.id ?? null, intoPlace),
			(n) => setProgress(t.progress.moving(n, list.length)),
		);
		setProgress(undefined);
		changed();
		if (r.failed.length === 0) {
			say({ text: movedMany(r.done.length), action: show });
			return;
		}
		setBulkResult({
			title: t.bulk.notMoved(r.failed.length, list.length),
			done: r.done.length > 0 ? movedMany(r.done.length) : t.bulk.noneMoved,
			stayed: r.failed.length === 1 ? t.bulk.movedStayedOne : t.bulk.movedStayedMany,
			failed: r.failed,
		});
	};

	// What can be done with the items selected: on a wide screen, the bar the list's header becomes with two or more;
	// on a phone, the bar at the bottom while choosing, with one or more.
	const several = choosing ? tickedItems.length >= 1 : tickedItems.length >= 2;
	const canMoveAll = several && tickedItems.every((i) => i.can?.includes("move"));
	const canDeleteAll = several && tickedItems.every((i) => i.can?.includes("delete"));
	const canZipAll = several && can.includes("download");
	const bar =
		placeId !== undefined && several && !choosing ? (
			<div className="flex flex-wrap items-center gap-2 py-1.5">
				<span className="mr-auto pl-3 font-semibold" aria-live="polite">
					{progress ?? t.selection.selected(tickedItems.length)}
				</span>
				{canMoveAll && (
					<Button small onClick={() => setMoving(tickedItems)} disabled={!!progress}>
						<FolderInput aria-hidden /> {t.common.move}
					</Button>
				)}
				{canZipAll && (
					<a
						href={api.zipUrl(placeId, folderId, tickedItems.map((i) => i.id))}
						download
						className={`${link} h-8 px-2.5 ${progress ? "pointer-events-none opacity-50" : ""}`}
					>
						<Download aria-hidden className="size-4" /> {t.common.downloadZip}
					</a>
				)}
				{canDeleteAll && (
					<Button small variant="danger" onClick={() => void removeSeveral(tickedItems)} disabled={!!progress}>
						<Trash2 aria-hidden /> {t.common.delete}
					</Button>
				)}
				<Button small variant="ghost" onClick={clearSelection} disabled={!!progress}>
					<X aria-hidden /> {t.selection.clear}
				</Button>
			</div>
		) : undefined;

	// Someone no longer in the member list, with the page still open: the Drive refuses all they ask (403, on a request
	// that only reads), so the page says why instead of showing what it still held. Reloading shows the same.
	const refused = [me, places, listing].map((q) => q.error).find((e) => e instanceof ApiError && e.status === 403);
	if (refused) {
		return (
			<main className="grid h-dvh place-items-center bg-background p-6 text-foreground max-md:p-4">
				<div className="max-w-md rounded-xl border border-border bg-card p-6">
					<h1 className="text-lg font-semibold">{t.app.closedTitle}</h1>
					<p className="mt-2 text-sm text-muted-foreground">{refused.message}</p>
					<p className="mt-2 text-sm text-muted-foreground">{t.app.closedNote}</p>
				</div>
			</main>
		);
	}

	return (
		<div className="flex h-dvh bg-background text-foreground">
			<Sidebar
				project={project}
				where={where}
				go={goTo}
				sharedFolders={sharedFolders}
				inShared={inPart}
				onUpload={canUpload ? uploads.add : undefined}
				onNewFolder={inFiles && can.includes("make-folder") ? () => setDialog("new-folder") : undefined}
				onNewSharedFolder={() => setDialog("new-shared-folder")}
				oversees={me.data?.can?.includes("oversee")}
				open={narrow && menuOpen}
				onClose={closeMenu}
			/>
			<div
				className="relative flex min-w-0 flex-1 flex-col"
				inert={narrow && menuOpen}
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
						say({ text: t.app.droppedUnreadable(err instanceof Error ? err.message : String(err)), failed: true }),
					);
				}}
			>
				<header className="flex h-14 shrink-0 items-center gap-2 border-b border-border bg-card px-2 md:hidden">
					<button
						type="button"
						onClick={() => setMenuOpen(true)}
						aria-label={t.app.openMenu}
						aria-expanded={menuOpen}
						className="grid size-11 place-items-center rounded-md hover:bg-muted"
					>
						<Menu aria-hidden className="size-5" />
					</button>
					<Wordmark className="text-lg" />
					<span className="min-w-0 truncate text-sm text-muted-foreground">{project ? t.app.driveOf(project) : t.app.drive}</span>
				</header>
				<main className={`min-h-0 flex-1 overflow-auto p-6 pb-24 max-md:p-4 ${choosing ? "max-md:pb-40" : "max-md:pb-24"}`}>
					{me.data?.rollback && <RollbackNotice key={me.data.rollback.at} rollback={me.data.rollback} />}
					<div className="mb-5">
						<SearchField query={query} onSearch={onSearch} onClear={endSearch} />
					</div>
					{where.view === "oversight" ? (
						me.data?.can?.includes("oversee") ? (
							<OversightView />
						) : (
							<p className="mt-16 text-center text-sm text-muted-foreground">{t.common.notHere}</p>
						)
					) : where.view === "search" ? (
						<>
							<h1 className="text-xl font-semibold break-words">{t.views.searchTitle(query.trim())}</h1>
							<p className="mt-1 text-sm text-muted-foreground">{t.views.searchNote}</p>
							<SearchList
								items={found.data?.items}
								loading={found.isPending}
								error={found.error}
								onRetry={() => void found.refetch()}
								onOpen={openFound}
								onGoTo={showFound}
								selected={foundKey}
								onSelect={(f) => setFoundKey(keyOfFound(f))}
							/>
							{found.data?.more && <p className="mt-3 text-sm text-muted-foreground">{t.views.searchMore(found.data.items.length)}</p>}
						</>
					) : where.view === "trash" ? (
						<>
							<h1 className="text-xl font-semibold">{t.app.trash}</h1>
							<p className="mt-1 text-sm text-muted-foreground">{t.views.trashNote}</p>
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
							<h1 className="text-xl font-semibold">{t.app.sharedWithMe}</h1>
							<p className="mt-1 text-sm text-muted-foreground">{t.views.sharedNote}</p>
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
								<p className="mt-3 text-sm text-muted-foreground">{narrow ? t.note.sharedHintPhone : t.note.sharedHint}</p>
							)}
						</>
					) : missingPlace ? (
						<div className="mt-16 flex flex-col items-center text-center text-sm text-muted-foreground">
							<p>{t.common.notHere}</p>
							<Button className="mt-3" onClick={() => goTo({ view: "files", place: null, folder: null })}>
								{t.app.goToMyFiles}
							</Button>
						</div>
					) : (
						<>
							<div className="flex flex-wrap items-start gap-3">
								<div className="min-w-0 flex-1 max-md:basis-full">
									<Breadcrumb
										placeName={fromTop ? placeName : undefined}
										place={where.view === "files" ? where.place : null}
										path={path}
										go={goTo}
									/>
								</div>
								{inFiles && placeId !== undefined && can.includes("download") && items && items.length > 0 && (
									<a href={api.zipUrl(placeId, current?.id ?? null)} download className={`${link} h-8 px-2.5`}>
										<Download aria-hidden className="size-4" /> {t.common.downloadZip}
									</a>
								)}
								{inFiles && canSeeHere && (
									<Button
										small
										onClick={() => {
											setTicked(new Set());
											setPlacePanel(true);
										}}
									>
										<Users aria-hidden /> {t.toolbar.whoCanSeeThis}
									</Button>
								)}
								{narrow && inFiles && items && items.length > 0 && !choosing && (
									<Button small onClick={() => startSelecting()}>
										<CheckSquare aria-hidden /> {t.toolbar.select}
									</Button>
								)}
							</div>
							<p className="mt-1 text-sm text-muted-foreground">{note}</p>
							{listing.data?.outside && listing.data.outside !== "on" && (
								<p className="mt-1 text-sm text-muted-foreground">
									{listing.data.outside === "project" ? t.note.offForProject : t.note.offForFolder}
								</p>
							)}
							{differs && acting && (
								<p role="status" className="mt-3 flex flex-wrap items-center gap-x-3 text-sm text-muted-foreground">
									{t.note.changedOnStorage}
									<Button small variant="ghost" onClick={showChanges}>
										{t.note.showChanges}
									</Button>
								</p>
							)}
							<ItemList
								placeId={placeId}
								items={items}
								loading={places.isPending || listing.isPending}
								error={places.error ?? listing.error}
								onRetry={() =>
									places.error ? places.refetch() : folderGone ? (fromTop ? openFolder(null) : goTo({ view: "files", place: null, folder: null })) : listing.refetch()
								}
								retryLabel={folderGone ? t.app.goTo(fromTop ? placeName : t.app.myFiles) : undefined}
								onOpen={openFolder}
								ticked={ticked}
								onSelect={select}
								onTick={tick}
								onTickAll={tickAll}
								bar={bar}
								me={me.data?.userId}
								members={members.data}
								canAdd={can.includes("upload")}
								selecting={choosing}
								onHold={(item) => startSelecting(item)}
							/>
							{items && items.length > 0 && <p className="mt-3 text-sm text-muted-foreground">{narrow ? t.note.hintPhone : t.note.hint}</p>}
						</>
					)}
				</main>
				{dragging && (
					<div className="pointer-events-none absolute inset-3 grid place-items-center rounded-xl border-2 border-dashed border-ring bg-card/85">
						<p className="flex items-center gap-2 font-semibold">
							<Upload aria-hidden className="size-5 text-muted-foreground" /> {t.app.dropHere(here)}
						</p>
					</div>
				)}
				{choosing && placeId !== undefined && (
					<PhoneBar
						count={tickedItems.length}
						progress={progress}
						onMove={canMoveAll ? () => setMoving(tickedItems) : undefined}
						zip={canZipAll ? api.zipUrl(placeId, folderId, tickedItems.map((i) => i.id)) : undefined}
						onDelete={canDeleteAll ? () => void removeSeveral(tickedItems) : undefined}
						onDone={clearSelection}
					/>
				)}
				<NoticeBar notice={notice} onClose={closeNotice} />
				<UploadsPanel uploads={uploads.uploads} onCancel={uploads.cancel} onClose={uploads.clear} raised={choosing} />
			</div>
			{selected && placeId !== undefined && where.view === "files" && (
				<DetailsPanel
					placeId={placeId}
					where={whereOf(rootName, path)}
					can={selected.can ?? []}
					me={me.data?.userId}
					item={selected}
					busy={busy}
					onClose={() => setTicked(new Set())}
					onOpen={openFolder}
					onRename={() => setDialog("rename")}
					onMove={() => setMoving([selected])}
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
					onAccessChanged={accessChanged}
				/>
			)}
			{sharedSelected && (
				<DetailsPanel
					key={keyOf(sharedSelected)}
					placeId={sharedSelected.place}
					where={t.app.sharedWithMe}
					can={sharedSelected.can ?? []}
					me={me.data?.userId}
					item={sharedSelected}
					onClose={() => setSharedKey(undefined)}
					onOpen={() => openShared(sharedSelected)}
					onAccessChanged={() => {
						void queryClient.invalidateQueries({ queryKey: ["shared-with-me"] });
						void queryClient.invalidateQueries({ queryKey: ["items", sharedSelected.place] });
					}}
				/>
			)}
			{foundSelected && (
				<DetailsPanel
					key={keyOfFound(foundSelected)}
					placeId={foundSelected.place}
					where={locationOf(foundSelected)}
					can={foundSelected.can ?? []}
					me={me.data?.userId}
					item={foundSelected}
					onClose={() => setFoundKey(undefined)}
					onOpen={() => openFound(foundSelected)}
					onShow={() => showFound(foundSelected)}
					onAccessChanged={() => {
						void queryClient.invalidateQueries({ queryKey: ["search"] });
						void queryClient.invalidateQueries({ queryKey: ["items", foundSelected.place] });
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
					title={t.dialogs.newFolder}
					description={t.dialogs.newFolderIn(whereOf(rootName, path))}
					initial={freeName(t.dialogs.newFolderName, items, true)}
					submitLabel={t.dialogs.makeFolder}
					onSubmit={async (name) => {
						await api.newFolder(placeId, folderId, name);
						refresh(folderId);
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{dialog === "new-shared-folder" && (
				<NameDialog
					title={t.dialogs.newSharedFolder}
					description={t.dialogs.newSharedFolderNote}
					initial=""
					submitLabel={t.dialogs.makeSharedFolder}
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
					title={t.dialogs.renameTitle(selected.name)}
					initial={selected.name}
					selectStem={selected.kind === "file"}
					submitLabel={t.common.rename}
					onSubmit={async (name) => {
						if (name === selected.name) return;
						await api.rename(placeId, selected.id, name);
						refresh(folderId);
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{moving && moving.length > 0 && placeId !== undefined && (
				<MoveDialog
					place={{ id: placeId, name: placeName }}
					root={fromTop ? null : (path[0] ?? null)}
					items={moving}
					path={path}
					otherPlaces={moving.every((i) => i.can?.includes("move-out")) ? moveInto : []}
					me={me.data?.userId}
					members={members.data}
					onMove={({ place: to, folder }) => moveTo(moving, to, folder)}
					progress={progress}
					onClose={() => setMoving(undefined)}
				/>
			)}
			{bulkResult && <BulkResultDialog {...bulkResult} onClose={() => setBulkResult(undefined)} />}
			{uploads.question && <ConflictDialog key={uploads.question.key} question={uploads.question} onAnswer={uploads.answer} />}
		</div>
	);
}

/**
 * On a phone, while choosing several: a bar at the bottom of the screen with how many are ticked (or how far a change
 * has got), what can be done with them, and Done. Each button is a large target, its icon over its word.
 */
function PhoneBar(props: {
	count: number;
	progress?: string;
	onMove?: () => void;
	zip?: string;
	onDelete?: () => void;
	onDone: () => void;
}) {
	const { count, progress, onMove, zip, onDelete, onDone } = props;
	const none = count === 0 || !!progress;
	const action =
		"flex min-h-14 flex-1 flex-col items-center justify-center gap-1 rounded-md px-1 text-xs leading-tight font-medium hover:bg-muted disabled:pointer-events-none disabled:opacity-40 aria-disabled:pointer-events-none aria-disabled:opacity-40";
	return (
		<section
			aria-label={t.selection.actions}
			className="absolute inset-x-0 bottom-0 z-10 border-t border-border bg-card px-2 pt-2 pb-[max(0.5rem,env(safe-area-inset-bottom))] shadow-[0_-4px_12px_rgb(0_0_0/0.06)]"
		>
			<div className="flex items-center gap-2 px-2">
				<p className="min-w-0 flex-1 truncate text-sm font-semibold" aria-live="polite">
					{progress ?? t.selection.selected(count)}
				</p>
				<Button small variant="ghost" onClick={onDone} disabled={!!progress}>
					{t.selection.done}
				</Button>
			</div>
			<div className="mt-1 flex gap-1">
				<button type="button" className={action} onClick={onMove} disabled={none || !onMove}>
					<FolderInput aria-hidden className="size-5" /> {t.common.move}
				</button>
				<a
					href={zip && !none ? zip : undefined}
					download
					aria-disabled={none || !zip}
					className={`${action} text-center`}
					role={zip && !none ? undefined : "link"}
				>
					<Download aria-hidden className="size-5" /> {t.common.downloadZip}
				</a>
				<button type="button" className={`${action} text-destructive`} onClick={onDelete} disabled={none || !onDelete}>
					<Trash2 aria-hidden className="size-5" /> {t.common.delete}
				</button>
			</div>
		</section>
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
	const crumb = "max-w-64 truncate underline-offset-4 hover:underline max-md:max-w-40 max-md:py-2";
	const toShared = (e: MouseEvent) => {
		if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
		e.preventDefault();
		go({ view: "shared" });
	};
	return (
		<nav aria-label={t.app.breadcrumb}>
			<ol className="flex min-w-0 flex-wrap items-center gap-1 text-xl font-semibold">
				<li>
					{placeName !== undefined ? (
						<a href={hrefOf({ view: "files", place, folder: null })} onClick={link(null)} className={`block ${crumb}`} title={placeName}>
							{placeName}
						</a>
					) : (
						// Given part of a place: it was found in "Shared with me", and nothing above it is theirs to see.
						<a href={hrefOf({ view: "shared" })} onClick={toShared} className={`block ${crumb}`}>
							{t.app.sharedWithMe}
						</a>
					)}
				</li>
				{path.map((f, i) => (
					<li key={f.id} className="flex min-w-0 items-center gap-1">
						<ChevronRight aria-hidden className="size-4 shrink-0 text-muted-foreground" />
						{i === path.length - 1 ? (
							<h1 aria-current="page" className="max-w-96 truncate max-md:max-w-full" title={f.name}>
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
