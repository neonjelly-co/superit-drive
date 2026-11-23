// The guests' page: signing in with a link mailed to them, "Shared with me", and what is in what was shared, with what
// their role lets them do. One plain column on every screen, a phone's too, in the guest's language (with the switch at
// the top). It calls only the guests' API (under /s/guest/api), where the Drive decides everything again.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Download, ExternalLink, Folder, FolderInput, FolderPlus, LogOut, PenLine, Trash2, Upload } from "lucide-react";
import { type FormEvent, type ReactNode, useCallback, useEffect, useId, useRef, useState } from "react";
import { ApiError, api, type FolderRef, type GuestItem, guestApi } from "./api";
import { Button } from "./components/button";
import { ConflictDialog } from "./components/conflict-dialog";
import { LanguageSwitch } from "./components/language-switch";
import { Dialog } from "./components/dialog";
import { FileIcon } from "./components/file-icon";
import { NameDialog } from "./components/name-dialog";
import { UploadsPanel } from "./components/uploads-panel";
import { Wordmark } from "./components/wordmark";
import { formatChanged, formatDayMonth, formatSize } from "./format";
import { fill, t } from "./i18n";
import { touchOnly } from "./narrow";
import { droppedFiles, useUploads } from "./uploads";

/** Where a guest is: their "Shared with me", or a file or folder (by place and id). It lives in the address. */
type Where = { view: "shared" } | { view: "item"; place: number; item: number };

const id = (v: string | null) => {
	const n = Number(v);
	return Number.isSafeInteger(n) && n > 0 ? n : null;
};

function read(): Where {
	const q = new URLSearchParams(window.location.search);
	const place = id(q.get("place"));
	const item = id(q.get("item"));
	return place && item ? { view: "item", place, item } : { view: "shared" };
}

const addressOf = (w: Where) => (w.view === "shared" ? "./" : `./?${new URLSearchParams({ place: String(w.place), item: String(w.item) })}`);

function useWhere(): [Where, (w: Where) => void] {
	const [where, setWhere] = useState(read);
	useEffect(() => {
		const onPop = () => setWhere(read());
		window.addEventListener("popstate", onPop);
		return () => window.removeEventListener("popstate", onPop);
	}, []);
	const go = useCallback((w: Where) => {
		window.history.pushState(null, "", addressOf(w));
		setWhere(w);
		window.scrollTo(0, 0);
	}, []);
	return [where, go];
}

/**
 * What a link from the Drive's mail carried after "#": the guest's address, for them to confirm (an invitation's link),
 * and, in a sign-in link, the code that signs them in. A browser never sends what is after "#" by itself.
 */
function fromLink(): { address: string; code: string } {
	const after = new URLSearchParams(window.location.hash.slice(1));
	return { address: after.get("as") ?? "", code: after.get("code") ?? "" };
}

/** The code of the sign-in link the page was opened with, to be used once: taking it removes it from the address. */
let linkCode: string | undefined;
function takeLinkCode(): string {
	if (linkCode === undefined) {
		const { address, code } = fromLink();
		linkCode = code;
		// The code works once: it leaves the address bar and the history, the address stays to be confirmed.
		if (code) window.history.replaceState(null, "", `${window.location.pathname}${window.location.search}#as=${encodeURIComponent(address)}`);
	}
	const code = linkCode;
	linkCode = "";
	return code;
}

/** Where the guest was going, for the sign-in link to lead back there: the item the address names, if any. */
function goingTo(): { place?: number; item?: number } {
	const q = new URLSearchParams(window.location.search);
	const place = Number(q.get("place"));
	const item = Number(q.get("item"));
	return Number.isInteger(place) && place > 0 && Number.isInteger(item) && item > 0 ? { place, item } : {};
}

const signedOut = (e: unknown) => e instanceof ApiError && e.status === 401;

/** The page: signed in or not, as the Drive says. */
export function GuestApp() {
	const queryClient = useQueryClient();
	const me = useQuery({ queryKey: ["guest-me"], queryFn: guestApi.me });
	// Any answer that says the session has ended (taken away, ended, signed out elsewhere) brings the sign-in back.
	useEffect(
		() =>
			queryClient.getQueryCache().subscribe((event) => {
				if (event.type === "updated" && event.query.queryKey[0] !== "guest-me" && signedOut(event.query.state.error)) {
					void queryClient.invalidateQueries({ queryKey: ["guest-me"] });
				}
			}),
		[queryClient],
	);
	// The sign-in stays on the page while the Drive is asked again who this is (coming back to the tab asks again, and
	// a guest leaves the tab to read their mail): asking again must not throw away the address typed.
	const out = useRef(false);
	if (!me.isPending) out.current = signedOut(me.error);
	if (out.current) return <SignIn onSignedIn={() => void queryClient.resetQueries()} />;
	if (me.isPending) return <Page>{t.common.loading}</Page>;
	if (me.error) return <Page><Problem error={me.error} /></Page>;
	return <SignedIn address={me.data.address} project={me.data.project} />;
}

/** One plain column, with the Drive's name and the switch to the other language at the top. */
function Page({ children, project, top }: { children: ReactNode; project?: string; top?: ReactNode }) {
	useEffect(() => {
		document.title = project ? t.guest.driveOf(project) : t.app.drive;
	}, [project]);
	return (
		<div className="flex min-h-dvh flex-col">
			<header className="border-b border-border bg-card">
				<div className="mx-auto flex max-w-3xl flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 max-md:py-2">
					<p className="flex min-w-0 items-baseline gap-2 text-sm">
						<Wordmark className="text-lg" />
						<span className="truncate text-muted-foreground">{project ? t.guest.driveOf(project) : t.app.drive}</span>
					</p>
					<div className="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-2">
						{top}
						<LanguageSwitch />
					</div>
				</div>
			</header>
			<main className="mx-auto w-full max-w-3xl flex-1 px-4 py-8">{children}</main>
		</div>
	);
}

function Problem({ error }: { error: unknown }) {
	// Not available while sharing outside the project is switched off: nothing went wrong, so it is said calmly.
	if (error instanceof ApiError && error.more.unavailable) {
		return (
			<p role="status" className="text-muted-foreground">
				{error.message}
			</p>
		);
	}
	return (
		<p role="alert" className="text-sm text-destructive">
			{error instanceof Error ? error.message : String(error)}
		</p>
	);
}

const field =
	"h-10 w-full min-w-0 rounded-md border border-input bg-card px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 max-md:h-11";

/**
 * Signing in: the guest confirms their address (filled in from the invitation's link) and is mailed a link that signs
 * them in. Opened with such a link, the page signs in with it at once. The Drive says the same whatever the address,
 * so the page does too.
 */
function SignIn({ onSignedIn }: { onSignedIn: () => void }) {
	const [address, setAddress] = useState(() => fromLink().address);
	const [sent, setSent] = useState<string>();
	const [problem, setProblem] = useState<string>();
	const [busy, setBusy] = useState(false);
	const addressId = useId();

	// A sign-in link: its code is used once, as the page opens.
	useEffect(() => {
		const code = takeLinkCode();
		if (!code) return;
		setBusy(true);
		guestApi
			.signIn(fromLink().address, code)
			.then(onSignedIn)
			.catch((err) => {
				setProblem(err instanceof Error ? err.message : String(err));
				setBusy(false);
			});
		// Once, with what the page was opened with.
	}, []);

	const ask = async (e?: FormEvent) => {
		e?.preventDefault();
		setBusy(true);
		setProblem(undefined);
		try {
			setSent((await guestApi.askForCode(address.trim(), goingTo())).message);
		} catch (err) {
			setProblem(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy(false);
		}
	};

	return (
		<Page>
			<h1 className="text-2xl font-bold">{t.guest.signInTitle}</h1>
			<p className="mt-2 text-muted-foreground">{t.guest.signInNote}</p>
			{sent === undefined ? (
				<form onSubmit={ask} className="mt-6 max-w-md">
					<label htmlFor={addressId} className="text-sm font-medium">
						{t.guest.yourAddress}
					</label>
					<input
						id={addressId}
						type="email"
						autoComplete="email"
						required
						value={address}
						onChange={(e) => setAddress(e.target.value)}
						className={`mt-1.5 ${field} font-mono`}
					/>
					<Button variant="primary" type="submit" className="mt-4" disabled={busy || !address.trim()}>
						{t.guest.sendLink}
					</Button>
				</form>
			) : (
				<div className="mt-6 max-w-md">
					<p role="status" className="text-sm">
						{sent}
					</p>
					<p className="mt-1 text-sm break-words text-muted-foreground">
						{fill(t.guest.sentTo, { address: <span className="font-mono">{address.trim()}</span> })}
					</p>
					<p className="mt-3 text-sm text-muted-foreground">{t.guest.openItHere}</p>
					<div className="mt-4 flex flex-wrap gap-2">
						<Button variant="ghost" onClick={() => setSent(undefined)} disabled={busy}>
							{t.guest.anotherAddress}
						</Button>
					</div>
				</div>
			)}
			{problem && (
				<p role="alert" className="mt-4 text-sm text-destructive">
					{problem}
				</p>
			)}
		</Page>
	);
}

function SignedIn({ address, project }: { address: string; project: string }) {
	const queryClient = useQueryClient();
	const [where, go] = useWhere();
	const signOut = async () => {
		await guestApi.signOut().catch(() => undefined);
		window.history.replaceState(null, "", "./");
		await queryClient.resetQueries();
	};
	return (
		<Page
			project={project}
			top={
				<>
					<span className="max-w-64 truncate font-mono text-sm text-muted-foreground max-md:max-w-full" title={address}>
						{address}
					</span>
					<Button small variant="ghost" onClick={() => void signOut()}>
						<LogOut aria-hidden /> {t.guest.signOut}
					</Button>
				</>
			}
		>
			{where.view === "shared" ? (
				<Shared go={go} project={project} />
			) : (
				<ItemView key={`${where.place}-${where.item}`} place={where.place} itemId={where.item} go={go} />
			)}
		</Page>
	);
}

/**
 * "Shared with me": everything shared with the guest that they may open now, the most recently shared first; and, while
 * the project has switched off sharing outside the project, that something else given to them is not available now.
 */
function Shared({ go, project }: { go: (w: Where) => void; project: string }) {
	const shared = useQuery({ queryKey: ["guest-shared"], queryFn: guestApi.sharedWithMe });
	const items = shared.data?.items;
	return (
		<>
			<h1 className="text-2xl font-bold">{t.app.sharedWithMe}</h1>
			{shared.data?.unavailable && (
				<p role="status" className="mt-3 text-muted-foreground">
					{t.guest.unavailable(project)}
				</p>
			)}
			{shared.error ? (
				<div className="mt-4">
					<Problem error={shared.error} />
				</div>
			) : !items ? (
				<p className="mt-4 text-muted-foreground">{t.common.loading}</p>
			) : items.length === 0 ? (
				<p className="mt-4 text-muted-foreground">{t.guest.nothingShared}</p>
			) : (
				<ul className="mt-6 overflow-hidden rounded-xl border border-border bg-card">
					{items.map((it) => (
						<li key={`${it.place}-${it.id}`} className="flex items-center gap-3 border-t border-border px-4 py-3 first:border-t-0 max-md:py-2">
							<FileIcon item={it} />
							<div className="min-w-0 flex-1">
								<button type="button" onClick={() => go({ view: "item", place: it.place, item: it.id })} className="max-w-full truncate text-left font-semibold underline-offset-4 hover:underline max-md:min-h-11">
									{it.name}
								</button>
								<p className="text-sm text-muted-foreground">
									{[it.sharedBy, it.role === "editor" ? t.common.editor : t.common.viewer, ...(it.until ? [t.guest.until(formatDayMonth(it.until))] : [])].join(" · ")}
								</p>
							</div>
							{it.kind === "file" && (
								<a
									href={api.downloadUrl(it.place, it.id)}
									download
									className="inline-flex shrink-0 items-center text-sm font-semibold underline-offset-4 hover:underline max-md:min-h-11"
								>
									{t.common.download}
								</a>
							)}
						</li>
					))}
				</ul>
			)}
		</>
	);
}

/** A file or folder: a folder's list, or a file's page. */
function ItemView({ place, itemId, go }: { place: number; itemId: number; go: (w: Where) => void }) {
	const item = useQuery({ queryKey: ["guest-item", place, itemId], queryFn: () => guestApi.item(place, itemId) });
	if (item.error) {
		return (
			<>
				<Crumbs path={[]} go={go} place={place} />
				<div className="mt-4">
					<Problem error={item.error} />
				</div>
			</>
		);
	}
	if (!item.data) return <p className="text-muted-foreground">{t.common.loading}</p>;
	const { item: it, path } = item.data;
	if (it.kind === "folder") return <FolderView place={place} folder={it} above={path} go={go} />;
	return (
		<>
			<Crumbs path={path} go={go} place={place} />
			<div className="mt-4 flex items-start gap-3">
				<FileIcon item={it} className="mt-1 size-6" />
				<div className="min-w-0">
					<h1 className="text-2xl font-bold break-words">{it.name}</h1>
					<p className="mt-1 text-muted-foreground">{it.size !== undefined ? formatSize(it.size) : ""}</p>
				</div>
			</div>
			<p className="mt-6 flex flex-wrap gap-2">
				{it.opens && (
					<a href={api.openUrl(place, it.id)} target="_blank" rel="noopener noreferrer" className={linkButton}>
						<ExternalLink aria-hidden className="size-4" /> {t.common.open}
					</a>
				)}
				<a href={api.downloadUrl(place, it.id)} download className={`${linkButton} bg-primary text-primary-foreground hover:bg-primary/80`}>
					<Download aria-hidden className="size-4" /> {t.common.download}
				</a>
			</p>
		</>
	);
}

const linkButton =
	"inline-flex h-9 items-center gap-1.5 rounded-md border border-border bg-card px-3 text-sm font-medium outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50 max-md:h-11 max-md:min-w-11 max-md:justify-center";

/** Where the guest is: "Shared with me", then the folders from the one given to them down. */
function Crumbs({ path, go, place }: { path: FolderRef[]; go: (w: Where) => void; place: number }) {
	return (
		<nav aria-label={t.common.folders} className="flex flex-wrap items-center gap-1 text-sm text-muted-foreground">
			<button type="button" onClick={() => go({ view: "shared" })} className="underline-offset-4 hover:underline max-md:min-h-11">
				{t.app.sharedWithMe}
			</button>
			{path.map((f) => (
				<span key={f.id} className="flex items-center gap-1">
					<ChevronRight aria-hidden className="size-3.5" />
					<button type="button" onClick={() => go({ view: "item", place, item: f.id })} className="max-w-48 truncate underline-offset-4 hover:underline max-md:min-h-11">
						{f.name}
					</button>
				</span>
			))}
		</nav>
	);
}

type Dialogs =
	| { kind: "new-folder" }
	| { kind: "rename"; item: GuestItem }
	| { kind: "move"; item: GuestItem }
	| { kind: "delete"; item: GuestItem }
	| null;

/** A folder given to the guest, or inside it: what is in it, and what their role lets them do with it. */
function FolderView({ place, folder, above, go }: { place: number; folder: GuestItem; above: FolderRef[]; go: (w: Where) => void }) {
	const queryClient = useQueryClient();
	const key = ["items", place, folder.id];
	const listing = useQuery({ queryKey: key, queryFn: () => guestApi.list(place, folder.id) });
	// Opening a folder has the Drive look at it on the storage; the list is asked for again once it has.
	const checking = listing.data?.checking;
	useEffect(() => {
		if (!checking) return;
		let stale = false;
		guestApi.list(place, folder.id, true).then(
			(fresh) => {
				if (!stale) queryClient.setQueryData(key, fresh);
			},
			() => undefined,
		);
		return () => {
			stale = true;
		};
	}, [checking, place, folder.id]);
	const items = listing.data?.items;
	const uploads = useUploads(place, folder.id, items);
	const [dialog, setDialog] = useState<Dialogs>(null);
	const [problem, setProblem] = useState<string>();
	const chooser = useRef<HTMLInputElement>(null);
	const can = listing.data?.can ?? folder.can;
	const canUpload = can.includes("upload-as-guest");
	const path = listing.data?.path ?? [...above, { id: folder.id, name: folder.name }];
	const changed = () => {
		void queryClient.invalidateQueries({ queryKey: key });
		void queryClient.invalidateQueries({ queryKey: ["guest-shared"] });
	};

	return (
		<div
			onDragOver={(e) => {
				if (canUpload) e.preventDefault();
			}}
			onDrop={(e) => {
				if (!canUpload) return;
				e.preventDefault();
				droppedFiles(e.dataTransfer).then(uploads.add, (err) => setProblem(err instanceof Error ? err.message : String(err)));
			}}
		>
			<Crumbs path={path.slice(0, -1)} go={go} place={place} />
			<h1 className="mt-2 text-2xl font-bold break-words">{folder.name}</h1>
			<div className="mt-4 flex flex-wrap gap-2">
				{canUpload && (
					<>
						<Button variant="primary" onClick={() => chooser.current?.click()}>
							<Upload aria-hidden /> {t.guest.upload}
						</Button>
						<input
							ref={chooser}
							type="file"
							multiple
							hidden
							tabIndex={-1}
							onChange={(e) => {
								const files = Array.from(e.currentTarget.files ?? []);
								e.currentTarget.value = "";
								if (files.length > 0) uploads.add(files.map((file) => ({ file, folders: [] })));
							}}
						/>
					</>
				)}
				{can.includes("make-folder-as-guest") && (
					<Button onClick={() => setDialog({ kind: "new-folder" })}>
						<FolderPlus aria-hidden /> {t.guest.newFolder}
					</Button>
				)}
				<a href={api.zipUrl(place, folder.id)} download className={linkButton}>
					<Download aria-hidden className="size-4" /> {t.guest.downloadAll}
				</a>
			</div>
			{canUpload && !touchOnly() && <p className="mt-2 text-sm text-muted-foreground">{t.guest.canDrop}</p>}
			{problem && (
				<div className="mt-3">
					<Problem error={new Error(problem)} />
				</div>
			)}
			{listing.error ? (
				<div className="mt-4">
					<Problem error={listing.error} />
				</div>
			) : !items ? (
				<p className="mt-4 text-muted-foreground">{t.common.loading}</p>
			) : items.length === 0 ? (
				<p className="mt-6 text-muted-foreground">{t.guest.empty}</p>
			) : (
				<ul className="mt-6 overflow-hidden rounded-xl border border-border bg-card">
					{items.map((it) => (
						<li key={it.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-border px-4 py-3 first:border-t-0">
							<FileIcon item={it} />
							<div className="min-w-0 flex-[1_1_12rem]">
								{it.kind === "folder" ? (
									<button type="button" onClick={() => go({ view: "item", place, item: it.id })} className="max-w-full truncate text-left font-semibold underline-offset-4 hover:underline max-md:min-h-11">
										{it.name}
									</button>
								) : (
									<a
										href={it.opens ? api.openUrl(place, it.id) : api.downloadUrl(place, it.id)}
										{...(it.opens ? { target: "_blank", rel: "noopener noreferrer" } : { download: true })}
										className="block max-w-full truncate font-semibold underline-offset-4 hover:underline max-md:flex max-md:min-h-11 max-md:items-center"
									>
										{it.name}
									</a>
								)}
								<p className="text-sm text-muted-foreground tabular-nums">
									{[it.size !== undefined ? formatSize(it.size) : "", it.changed ? formatChanged(it.changed) : ""].filter(Boolean).join(" · ")}
								</p>
							</div>
							<div className="flex flex-wrap gap-1">
								{it.kind === "file" && (
									<a href={api.downloadUrl(place, it.id)} download title={t.list.downloadNamed(it.name)} aria-label={t.list.downloadNamed(it.name)} className={`${linkButton} h-8 px-2.5`}>
										<Download aria-hidden className="size-4" />
									</a>
								)}
								{it.can.includes("rename-as-guest") && (
									<Button small variant="ghost" aria-label={t.guest.renameNamed(it.name)} title={t.common.rename} onClick={() => setDialog({ kind: "rename", item: it })}>
										<PenLine aria-hidden />
									</Button>
								)}
								{it.can.includes("move-as-guest") && (
									<Button small variant="ghost" aria-label={t.guest.moveNamed(it.name)} title={t.common.move} onClick={() => setDialog({ kind: "move", item: it })}>
										<FolderInput aria-hidden />
									</Button>
								)}
								{it.can.includes("delete-as-guest") && (
									<Button small variant="ghost" aria-label={t.guest.deleteNamed(it.name)} title={t.common.delete} onClick={() => setDialog({ kind: "delete", item: it })}>
										<Trash2 aria-hidden className="text-destructive" />
									</Button>
								)}
							</div>
						</li>
					))}
				</ul>
			)}

			<UploadsPanel uploads={uploads.uploads} onCancel={uploads.cancel} onClose={uploads.clear} />
			{uploads.question && <ConflictDialog key={uploads.question.key} question={uploads.question} onAnswer={uploads.answer} />}
			{dialog?.kind === "new-folder" && (
				<NameDialog
					title={t.dialogs.newFolder}
					initial=""
					submitLabel={t.dialogs.makeFolder}
					onSubmit={async (name) => {
						await api.newFolder(place, folder.id, name);
						changed();
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{dialog?.kind === "rename" && (
				<NameDialog
					title={t.dialogs.renameTitle(dialog.item.name)}
					initial={dialog.item.name}
					selectStem={dialog.item.kind === "file"}
					submitLabel={t.common.rename}
					onSubmit={async (name) => {
						await api.rename(place, dialog.item.id, name);
						changed();
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{dialog?.kind === "move" && (
				<MoveDialog
					place={place}
					item={dialog.item}
					root={path[0] ?? { id: folder.id, name: folder.name }}
					from={folder.id}
					onMoved={changed}
					onClose={() => setDialog(null)}
				/>
			)}
			{dialog?.kind === "delete" && (
				<DeleteDialog
					item={dialog.item}
					onDelete={async () => {
						await guestApi.remove(place, dialog.item.id);
						changed();
					}}
					onClose={() => setDialog(null)}
				/>
			)}
		</div>
	);
}

/** Asks before deleting: what a guest deletes goes to the trash of whoever shared it, not to one of theirs. */
function DeleteDialog({ item, onDelete, onClose }: { item: GuestItem; onDelete: () => Promise<unknown>; onClose: () => void }) {
	const [problem, setProblem] = useState<string>();
	const [busy, setBusy] = useState(false);
	return (
		<Dialog
			title={t.guest.deleteTitle(item.name)}
			description={t.guest.deleteNote}
			onClose={onClose}
			onSubmit={async () => {
				setBusy(true);
				try {
					await onDelete();
					onClose();
				} catch (e) {
					setProblem(e instanceof Error ? e.message : String(e));
					setBusy(false);
				}
			}}
			footer={
				<>
					<Button variant="ghost" onClick={onClose}>
						{t.common.cancel}
					</Button>
					<Button variant="danger" type="submit" disabled={busy}>
						<Trash2 aria-hidden /> {t.common.delete}
					</Button>
				</>
			}
		>
			{problem && <Problem error={new Error(problem)} />}
		</Dialog>
	);
}

/**
 * Moving inside what was given to the guest: the folders from the one given to them down, one level at a time; the
 * Drive refuses any other.
 */
function MoveDialog(props: { place: number; item: GuestItem; root: FolderRef; from: number; onMoved: () => void; onClose: () => void }) {
	const { place, item, root, from, onMoved, onClose } = props;
	const [trail, setTrail] = useState<FolderRef[]>([root]);
	const here = trail[trail.length - 1] ?? root;
	const listing = useQuery({ queryKey: ["items", place, here.id], queryFn: () => guestApi.list(place, here.id) });
	const [problem, setProblem] = useState<string>();
	const [busy, setBusy] = useState(false);
	const folders = (listing.data?.items ?? []).filter((it) => it.kind === "folder" && it.id !== item.id);
	const allowed = here.id !== from && listing.data?.can.includes("upload-as-guest");
	return (
		<Dialog
			wide
			title={t.move.titleOne(item.name)}
			description={t.guest.moveNote}
			onClose={onClose}
			onSubmit={async () => {
				setBusy(true);
				try {
					await guestApi.move(place, item.id, here.id);
					onMoved();
					onClose();
				} catch (e) {
					setProblem(e instanceof Error ? e.message : String(e));
					setBusy(false);
				}
			}}
			footer={
				<>
					<Button variant="ghost" onClick={onClose}>
						{t.common.cancel}
					</Button>
					<Button variant="primary" type="submit" disabled={busy || !allowed}>
						{t.move.moveHere}
					</Button>
				</>
			}
		>
			<nav aria-label={t.common.folders} className="flex flex-wrap items-center gap-1 text-sm text-muted-foreground">
				{trail.map((f, i) => (
					<span key={f.id} className="flex items-center gap-1">
						{i > 0 && <ChevronRight aria-hidden className="size-3.5" />}
						<button type="button" onClick={() => setTrail(trail.slice(0, i + 1))} className="underline-offset-4 hover:underline max-md:min-h-11">
							{f.name}
						</button>
					</span>
				))}
			</nav>
			<ul className="mt-3 max-h-72 overflow-auto rounded-md border border-border">
				{listing.isPending ? (
					<li className="px-3 py-2 text-sm text-muted-foreground">{t.common.loading}</li>
				) : folders.length === 0 ? (
					<li className="px-3 py-2 text-sm text-muted-foreground">{t.guest.noFolders}</li>
				) : (
					folders.map((f) => (
						<li key={f.id}>
							<button
								type="button"
								onClick={() => setTrail([...trail, { id: f.id, name: f.name }])}
								className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-muted max-md:min-h-11"
							>
								<Folder aria-hidden className="size-4 text-muted-foreground" />
								<span className="truncate">{f.name}</span>
							</button>
						</li>
					))
				)}
			</ul>
			{problem && (
				<div className="mt-3">
					<Problem error={new Error(problem)} />
				</div>
			)}
		</Dialog>
	);
}
