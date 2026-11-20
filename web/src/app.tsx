import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Download, FolderInput, Trash2, Upload, Users, X } from "lucide-react";
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
import { keyOf, SharedList } from "./components/shared-list";
import { NameDialog } from "./components/name-dialog";
import { OversightView } from "./components/oversight-view";
import { type Notice, NoticeBar } from "./components/notice";
import { RollbackNotice } from "./components/rollback-notice";
import { SearchField } from "./components/search-field";
import { keyOfFound, locationOf, SearchList } from "./components/search-list";
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

/** Whether two lists of a folder show the same: the same items, as they are, and the same path and rights. */
const sameList = (a: Listing, b: Listing) =>
	JSON.stringify({ ...a, checking: false }) === JSON.stringify({ ...b, checking: false });

/** "1 item", "3 items". */
const itemsCount = (n: number) => (n === 1 ? "1 item" : `${n} items`);

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
	// list count.
	const [ticked, setTicked] = useState<ReadonlySet<number>>(new Set());
	const tickedItems = items?.filter((i) => ticked.has(i.id)) ?? [];
	const selected = tickedItems.length === 1 ? tickedItems[0] : undefined;
	const [placePanel, setPlacePanel] = useState(false);
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
		ticked.size > 0 || dialog !== null || moving !== undefined || busy || progress !== undefined || bulkResult !== undefined || uploads.question !== undefined;
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

	const goTo = useCallback(
		(w: Where, how?: { replace?: boolean }) => {
			setTicked(new Set());
			setSharedKey(undefined);
			setFoundKey(undefined);
			setPlacePanel(false);
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

	const current = path.at(-1);
	const here = current?.name ?? placeName;
	const project = me.data?.project;
	useEffect(() => {
		const name =
			where.view === "trash"
				? "Trash"
				: where.view === "shared"
					? "Shared with me"
					: where.view === "search"
						? "Search"
						: where.view === "oversight"
							? "Sharing and space"
							: here;
		document.title = project ? `${name} · ${project}` : name;
	}, [where.view, here, project]);

	// Escape clears the selection, unless something else (a dialog, a field) takes it.
	const anyTicked = tickedItems.length > 0;
	useEffect(() => {
		if (!anyTicked) return;
		const clear = (e: KeyboardEvent) => {
			const target = e.target as HTMLElement | null;
			if (e.key !== "Escape" || e.defaultPrevented || document.querySelector("dialog[open]")) return;
			if (target?.closest("input:not([type=checkbox]), textarea, select")) return;
			setTicked(new Set());
		};
		window.addEventListener("keydown", clear);
		return () => window.removeEventListener("keydown", clear);
	}, [anyTicked]);

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
			setTicked(new Set());
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

	/** Deletes every item selected, one request each, and says what happened: Undo, or which were not deleted and why. */
	const removeSeveral = async (list: Item[]) => {
		if (placeId === undefined || list.length === 0) return;
		const from = { id: placeId, name: placeName };
		setProgress(`Moving to the trash: 0 of ${list.length}`);
		const r = await eachOf(list, (it) => api.remove(from.id, it.id), (n) => setProgress(`Moving to the trash: ${n} of ${list.length}`));
		setProgress(undefined);
		setTicked(new Set());
		refresh(folderId);
		void queryClient.invalidateQueries({ queryKey: ["trash", from.id] });
		const undo = { label: "Undo", run: () => void restoreSeveral(from, r.done) };
		if (r.failed.length === 0) {
			say({ text: `${itemsCount(r.done.length)} moved to the trash.`, action: undo });
			return;
		}
		setBulkResult({
			title: `${r.failed.length} of ${itemsCount(list.length)} not deleted`,
			done: r.done.length > 0 ? `${itemsCount(r.done.length)} moved to the trash.` : "Nothing was moved to the trash.",
			stayed: r.failed.length === 1 ? "This one was not, and is still here:" : "These were not, and are still here:",
			failed: r.failed,
			undo: r.done.length > 0 ? { label: "Undo the delete", run: undo.run } : undefined,
		});
	};

	/** Brings several items back from a place's trash (Undo after deleting several), and says what happened. */
	const restoreSeveral = async (from: { id: number; name: string }, list: TrashItem[]) => {
		setProgress(`Restoring: 0 of ${list.length}`);
		const r = await eachOf(list, (t) => api.restore(from.id, t.id), (n) => setProgress(`Restoring: ${n} of ${list.length}`));
		setProgress(undefined);
		void queryClient.invalidateQueries({ queryKey: ["items", from.id] });
		void queryClient.invalidateQueries({ queryKey: ["usage"] });
		void queryClient.invalidateQueries({ queryKey: ["trash", from.id] });
		if (r.failed.length === 0) {
			say({ text: `${itemsCount(r.done.length)} back where they were.` });
			return;
		}
		setBulkResult({
			title: `${r.failed.length} of ${itemsCount(list.length)} not restored`,
			done: r.done.length > 0 ? `${itemsCount(r.done.length)} back where they were.` : "Nothing was restored.",
			stayed: r.failed.length === 1 ? "This one is still in the trash:" : "These are still in the trash:",
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

	/**
	 * Moves what the Move dialog moves into a folder of this place or, from the person's own My files, of a shared
	 * folder: one request per item. A single item that cannot move keeps the dialog open with the reason; of several,
	 * those that move do, and a dialog then says which did not, and why.
	 */
	const moveTo = async (list: Item[], to: { id: number; name: string }, folder: FolderRef | null) => {
		if (placeId === undefined) return;
		const there = folder?.name ?? to.name;
		const intoPlace = to.id === placeId ? undefined : to.id;
		const project = intoPlace === undefined ? "" : list.length === 1 ? " It belongs to the project now." : " They belong to the project now.";
		const show = {
			label: "Show",
			run: () => (intoPlace === undefined ? openFolder(folder) : goTo({ view: "files", place: to.id, folder: folder?.id ?? null })),
		};
		const changed = () => {
			setTicked(new Set());
			refresh(folderId);
			if (intoPlace === undefined) refresh(folder?.id ?? null);
			else {
				void queryClient.invalidateQueries({ queryKey: ["items", to.id] });
				void queryClient.invalidateQueries({ queryKey: ["access"] });
			}
		};
		const only = list.length === 1 ? list[0] : undefined;
		if (only) {
			await api.move(placeId, only.id, folder?.id ?? null, intoPlace);
			changed();
			say({ text: `“${only.name}” was moved to ${there}.${project}`, action: show });
			return;
		}
		setProgress(`Moving: 0 of ${list.length}`);
		const r: BulkOutcome<unknown> = await eachOf(
			list,
			(it) => api.move(placeId, it.id, folder?.id ?? null, intoPlace),
			(n) => setProgress(`Moving: ${n} of ${list.length}`),
		);
		setProgress(undefined);
		changed();
		if (r.failed.length === 0) {
			say({ text: `${itemsCount(r.done.length)} moved to ${there}.${project}`, action: show });
			return;
		}
		setBulkResult({
			title: `${r.failed.length} of ${itemsCount(list.length)} not moved`,
			done: r.done.length > 0 ? `${itemsCount(r.done.length)} moved to ${there}.${project}` : "Nothing was moved.",
			stayed: r.failed.length === 1 ? "This one was not, and is still where it was:" : "These were not, and are still where they were:",
			failed: r.failed,
		});
	};

	// The bar the list's header becomes with two or more items selected.
	const several = tickedItems.length >= 2;
	const canMoveAll = several && tickedItems.every((i) => i.can?.includes("move"));
	const canDeleteAll = several && tickedItems.every((i) => i.can?.includes("delete"));
	const bar =
		placeId !== undefined && several ? (
			<div className="flex flex-wrap items-center gap-2 py-1.5">
				<span className="mr-auto pl-3 font-semibold" aria-live="polite">
					{progress ?? `${tickedItems.length} selected`}
				</span>
				{canMoveAll && (
					<Button small onClick={() => setMoving(tickedItems)} disabled={!!progress}>
						<FolderInput aria-hidden /> Move
					</Button>
				)}
				{can.includes("download") && (
					<a
						href={api.zipUrl(placeId, folderId, tickedItems.map((i) => i.id))}
						download
						className={`${link} h-8 px-2.5 ${progress ? "pointer-events-none opacity-50" : ""}`}
					>
						<Download aria-hidden className="size-4" /> Download as zip
					</a>
				)}
				{canDeleteAll && (
					<Button small variant="danger" onClick={() => void removeSeveral(tickedItems)} disabled={!!progress}>
						<Trash2 aria-hidden /> Delete
					</Button>
				)}
				<Button small variant="ghost" onClick={() => setTicked(new Set())} disabled={!!progress}>
					<X aria-hidden /> Clear
				</Button>
			</div>
		) : undefined;

	// Someone no longer in the member list, with the page still open: the Drive refuses all they ask (403, on a request
	// that only reads), so the page says why instead of showing what it still held. Reloading shows the same.
	const refused = [me, places, listing].map((q) => q.error).find((e) => e instanceof ApiError && e.status === 403);
	if (refused) {
		return (
			<main className="grid h-screen place-items-center bg-background p-6 text-foreground">
				<div className="max-w-md rounded-xl border border-border bg-card p-6">
					<h1 className="text-lg font-semibold">The Drive is closed to you</h1>
					<p className="mt-2 text-sm text-muted-foreground">{refused.message}</p>
					<p className="mt-2 text-sm text-muted-foreground">
						If you think this is a mistake, ask a project admin. If you are added back, your files are here as you left
						them, unless they were handed to someone else or deleted.
					</p>
				</div>
			</main>
		);
	}

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
				oversees={me.data?.can?.includes("oversee")}
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
					{me.data?.rollback && <RollbackNotice key={me.data.rollback.at} rollback={me.data.rollback} />}
					<div className="mb-5">
						<SearchField query={query} onSearch={onSearch} onClear={endSearch} />
					</div>
					{where.view === "oversight" ? (
						me.data?.can?.includes("oversee") ? (
							<OversightView />
						) : (
							<p className="mt-16 text-center text-sm text-muted-foreground">This is not in the Drive, or you have no access to it.</p>
						)
					) : where.view === "search" ? (
						<>
							<h1 className="text-xl font-semibold break-words">Search: “{query.trim()}”</h1>
							<p className="mt-1 text-sm text-muted-foreground">
								Names of files and folders you can see, in any case and with or without accents. What is inside files is
								not searched.
							</p>
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
							{found.data?.more && (
								<p className="mt-3 text-sm text-muted-foreground">
									These are the best {found.data.items.length}. More names have this in them: type more of the name to find
									the one you want.
								</p>
							)}
						</>
					) : where.view === "trash" ? (
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
											setTicked(new Set());
											setPlacePanel(true);
										}}
									>
										<Users aria-hidden /> Who can see this
									</Button>
								)}
							</div>
							<p className="mt-1 text-sm text-muted-foreground">{note}</p>
							{listing.data?.outside && listing.data.outside !== "on" && (
								<p className="mt-1 text-sm text-muted-foreground">
									Sharing outside the project is switched off{" "}
									{listing.data.outside === "project" ? "for the whole project" : "for this shared folder"}: public links here open
									nothing and guests get nothing.
								</p>
							)}
							{differs && acting && (
								<p role="status" className="mt-3 flex items-center gap-3 text-sm text-muted-foreground">
									This folder changed on the Drive storage.
									<Button small variant="ghost" onClick={showChanges}>
										Show the changes
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
								retryLabel={folderGone ? `Go to ${fromTop ? placeName : "My files"}` : undefined}
								onOpen={openFolder}
								ticked={ticked}
								onSelect={select}
								onTick={tick}
								onTickAll={tickAll}
								bar={bar}
								me={me.data?.userId}
								members={members.data}
								canAdd={can.includes("upload")}
							/>
							{items && items.length > 0 && (
								<p className="mt-3 text-sm text-muted-foreground">
									Click a name to open it. Click elsewhere on a row to see its details. Tick several to move, download or
									delete them together.
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
					where="Shared with me"
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
