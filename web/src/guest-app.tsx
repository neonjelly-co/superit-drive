// The guests' page: signing in with a code mailed to them, "Shared with me", and what is in what was shared, with what
// their role lets them do. One plain column on every screen. It calls only the guests' API (under /s/guest/api), where
// the Drive decides everything again.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Download, ExternalLink, Folder, FolderInput, FolderPlus, LogOut, PenLine, Trash2, Upload } from "lucide-react";
import { type FormEvent, type ReactNode, useCallback, useEffect, useId, useRef, useState } from "react";
import { ApiError, api, type FolderRef, type GuestItem, guestApi } from "./api";
import { Button } from "./components/button";
import { ConflictDialog } from "./components/conflict-dialog";
import { Dialog } from "./components/dialog";
import { FileIcon } from "./components/file-icon";
import { NameDialog } from "./components/name-dialog";
import { UploadsPanel } from "./components/uploads-panel";
import { Wordmark } from "./components/wordmark";
import { formatChanged, formatSize } from "./format";
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

/** The address the invitation's link carried after "#as=", for the guest to confirm. */
function invitedAddress(): string {
	const hash = window.location.hash;
	if (!hash.startsWith("#as=")) return "";
	try {
		return decodeURIComponent(hash.slice(4));
	} catch {
		return "";
	}
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
					void me.refetch();
				}
			}),
		[queryClient, me],
	);
	if (me.isPending) return <Page>Loading…</Page>;
	if (me.error && signedOut(me.error)) return <SignIn onSignedIn={() => void queryClient.resetQueries()} />;
	if (me.error) return <Page><Problem error={me.error} /></Page>;
	return <SignedIn address={me.data.address} project={me.data.project} />;
}

/** One plain column, with the Drive's name at the top. */
function Page({ children, project, top }: { children: ReactNode; project?: string; top?: ReactNode }) {
	useEffect(() => {
		document.title = project ? `Drive of ${project}` : "Drive";
	}, [project]);
	return (
		<div className="flex min-h-screen flex-col">
			<header className="border-b border-border bg-card">
				<div className="mx-auto flex max-w-3xl flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
					<p className="flex items-baseline gap-2 text-sm">
						<Wordmark className="text-lg" />
						<span className="text-muted-foreground">Drive{project ? ` of ${project}` : ""}</span>
					</p>
					<div className="ml-auto flex items-center gap-2">{top}</div>
				</div>
			</header>
			<main className="mx-auto w-full max-w-3xl flex-1 px-4 py-8">{children}</main>
		</div>
	);
}

function Problem({ error }: { error: unknown }) {
	return (
		<p role="alert" className="text-sm text-destructive">
			{error instanceof Error ? error.message : String(error)}
		</p>
	);
}

const field =
	"h-10 w-full min-w-0 rounded-md border border-input bg-card px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50";

/**
 * Signing in: the guest confirms their address (filled in from the invitation's link), is mailed a code, and types it.
 * The Drive says the same whatever the address, so the page does too.
 */
function SignIn({ onSignedIn }: { onSignedIn: () => void }) {
	const [address, setAddress] = useState(invitedAddress);
	const [sent, setSent] = useState<string>();
	const [code, setCode] = useState("");
	const [problem, setProblem] = useState<string>();
	const [busy, setBusy] = useState(false);
	const addressId = useId();
	const codeId = useId();

	const ask = async (e?: FormEvent) => {
		e?.preventDefault();
		setBusy(true);
		setProblem(undefined);
		try {
			setSent((await guestApi.askForCode(address.trim())).message);
			setCode("");
		} catch (err) {
			setProblem(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy(false);
		}
	};
	const signIn = async (e: FormEvent) => {
		e.preventDefault();
		setBusy(true);
		setProblem(undefined);
		try {
			await guestApi.signIn(address.trim(), code);
			onSignedIn();
		} catch (err) {
			setProblem(err instanceof Error ? err.message : String(err));
			setBusy(false);
		}
	};

	return (
		<Page>
			<h1 className="text-2xl font-bold">Sign in to see what was shared with you</h1>
			<p className="mt-2 text-muted-foreground">
				Someone in this project shared files with your email address. Confirm it, and we mail you a code. You need no
				account and no password.
			</p>
			{sent === undefined ? (
				<form onSubmit={ask} className="mt-6 max-w-md">
					<label htmlFor={addressId} className="text-sm font-medium">
						Your email address
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
						Send me a code
					</Button>
				</form>
			) : (
				<form onSubmit={signIn} className="mt-6 max-w-md">
					<p role="status" className="text-sm">
						{sent}
					</p>
					<p className="mt-1 text-sm text-muted-foreground">
						Sent to <span className="font-mono">{address.trim()}</span>. Check your spam folder too.
					</p>
					<label htmlFor={codeId} className="mt-5 block text-sm font-medium">
						The code from the mail
					</label>
					<input
						id={codeId}
						inputMode="numeric"
						autoComplete="one-time-code"
						pattern="[0-9 ]*"
						maxLength={7}
						required
						value={code}
						onChange={(e) => setCode(e.target.value)}
						className={`mt-1.5 ${field} max-w-48 font-mono tracking-widest`}
					/>
					<div className="mt-4 flex flex-wrap gap-2">
						<Button variant="primary" type="submit" disabled={busy || code.replace(/\s/g, "").length !== 6}>
							Sign in
						</Button>
						<Button variant="ghost" onClick={() => void ask()} disabled={busy}>
							Send another code
						</Button>
						<Button variant="ghost" onClick={() => setSent(undefined)} disabled={busy}>
							Use another address
						</Button>
					</div>
				</form>
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
					<span className="max-w-64 truncate font-mono text-sm text-muted-foreground" title={address}>
						{address}
					</span>
					<Button small variant="ghost" onClick={() => void signOut()}>
						<LogOut aria-hidden /> Sign out
					</Button>
				</>
			}
		>
			{where.view === "shared" ? <Shared go={go} /> : <ItemView key={`${where.place}-${where.item}`} place={where.place} itemId={where.item} go={go} />}
		</Page>
	);
}

const untilDay = new Intl.DateTimeFormat("en-GB", { dateStyle: "long" });

/** "Shared with me": everything shared with the guest that they may open now, the most recently shared first. */
function Shared({ go }: { go: (w: Where) => void }) {
	const shared = useQuery({ queryKey: ["guest-shared"], queryFn: guestApi.sharedWithMe });
	return (
		<>
			<h1 className="text-2xl font-bold">Shared with me</h1>
			{shared.error ? (
				<div className="mt-4">
					<Problem error={shared.error} />
				</div>
			) : !shared.data ? (
				<p className="mt-4 text-muted-foreground">Loading…</p>
			) : shared.data.length === 0 ? (
				<p className="mt-4 text-muted-foreground">Nothing on this Drive is shared with you now.</p>
			) : (
				<ul className="mt-6 overflow-hidden rounded-xl border border-border bg-card">
					{shared.data.map((it) => (
						<li key={`${it.place}-${it.id}`} className="flex items-center gap-3 border-t border-border px-4 py-3 first:border-t-0">
							<FileIcon item={it} />
							<div className="min-w-0 flex-1">
								<button type="button" onClick={() => go({ view: "item", place: it.place, item: it.id })} className="max-w-full truncate text-left font-semibold underline-offset-4 hover:underline">
									{it.name}
								</button>
								<p className="text-sm text-muted-foreground">
									{it.sharedBy} · {it.role === "editor" ? "Editor" : "Viewer"}
									{it.until ? ` · until ${untilDay.format(new Date(it.until))}` : ""}
								</p>
							</div>
							{it.kind === "file" && (
								<a href={api.downloadUrl(it.place, it.id)} download className="shrink-0 text-sm font-semibold underline-offset-4 hover:underline">
									Download
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
	if (!item.data) return <p className="text-muted-foreground">Loading…</p>;
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
						<ExternalLink aria-hidden className="size-4" /> Open
					</a>
				)}
				<a href={api.downloadUrl(place, it.id)} download className={`${linkButton} bg-primary text-primary-foreground hover:bg-primary/80`}>
					<Download aria-hidden className="size-4" /> Download
				</a>
			</p>
		</>
	);
}

const linkButton =
	"inline-flex h-9 items-center gap-1.5 rounded-md border border-border bg-card px-3 text-sm font-medium outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50";

/** Where the guest is: "Shared with me", then the folders from the one given to them down. */
function Crumbs({ path, go, place }: { path: FolderRef[]; go: (w: Where) => void; place: number }) {
	return (
		<nav aria-label="Folders" className="flex flex-wrap items-center gap-1 text-sm text-muted-foreground">
			<button type="button" onClick={() => go({ view: "shared" })} className="underline-offset-4 hover:underline">
				Shared with me
			</button>
			{path.map((f) => (
				<span key={f.id} className="flex items-center gap-1">
					<ChevronRight aria-hidden className="size-3.5" />
					<button type="button" onClick={() => go({ view: "item", place, item: f.id })} className="max-w-48 truncate underline-offset-4 hover:underline">
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
		// eslint-disable-next-line react-hooks/exhaustive-deps
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
								e.currentTarget.value = "";
								if (files.length > 0) uploads.add(files.map((file) => ({ file, folders: [] })));
							}}
						/>
					</>
				)}
				{can.includes("make-folder-as-guest") && (
					<Button onClick={() => setDialog({ kind: "new-folder" })}>
						<FolderPlus aria-hidden /> New folder
					</Button>
				)}
				<a href={api.zipUrl(place, folder.id)} download className={linkButton}>
					<Download aria-hidden className="size-4" /> Download all as zip
				</a>
			</div>
			{canUpload && <p className="mt-2 text-sm text-muted-foreground">You can also drop files here.</p>}
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
				<p className="mt-4 text-muted-foreground">Loading…</p>
			) : items.length === 0 ? (
				<p className="mt-6 text-muted-foreground">This folder is empty.</p>
			) : (
				<ul className="mt-6 overflow-hidden rounded-xl border border-border bg-card">
					{items.map((it) => (
						<li key={it.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-border px-4 py-3 first:border-t-0">
							<FileIcon item={it} />
							<div className="min-w-0 flex-[1_1_12rem]">
								{it.kind === "folder" ? (
									<button type="button" onClick={() => go({ view: "item", place, item: it.id })} className="max-w-full truncate text-left font-semibold underline-offset-4 hover:underline">
										{it.name}
									</button>
								) : (
									<a
										href={it.opens ? api.openUrl(place, it.id) : api.downloadUrl(place, it.id)}
										{...(it.opens ? { target: "_blank", rel: "noopener noreferrer" } : { download: true })}
										className="block max-w-full truncate font-semibold underline-offset-4 hover:underline"
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
									<a href={api.downloadUrl(place, it.id)} download title={`Download ${it.name}`} aria-label={`Download ${it.name}`} className={`${linkButton} h-8 px-2.5`}>
										<Download aria-hidden className="size-4" />
									</a>
								)}
								{it.can.includes("rename-as-guest") && (
									<Button small variant="ghost" aria-label={`Rename ${it.name}`} title="Rename" onClick={() => setDialog({ kind: "rename", item: it })}>
										<PenLine aria-hidden />
									</Button>
								)}
								{it.can.includes("move-as-guest") && (
									<Button small variant="ghost" aria-label={`Move ${it.name}`} title="Move" onClick={() => setDialog({ kind: "move", item: it })}>
										<FolderInput aria-hidden />
									</Button>
								)}
								{it.can.includes("delete-as-guest") && (
									<Button small variant="ghost" aria-label={`Delete ${it.name}`} title="Delete" onClick={() => setDialog({ kind: "delete", item: it })}>
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
					title="New folder"
					initial=""
					submitLabel="Make folder"
					onSubmit={async (name) => {
						await api.newFolder(place, folder.id, name);
						changed();
					}}
					onClose={() => setDialog(null)}
				/>
			)}
			{dialog?.kind === "rename" && (
				<NameDialog
					title={`Rename “${dialog.item.name}”`}
					initial={dialog.item.name}
					selectStem={dialog.item.kind === "file"}
					submitLabel="Rename"
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
			title={`Delete “${item.name}”?`}
			description="It goes to the trash of whoever shared it with you, who can restore it for 30 days. You cannot undo it yourself."
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
						Cancel
					</Button>
					<Button variant="danger" type="submit" disabled={busy}>
						<Trash2 aria-hidden /> Delete
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
			title={`Move “${item.name}”`}
			description="Choose a folder inside what was shared with you."
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
						Cancel
					</Button>
					<Button variant="primary" type="submit" disabled={busy || !allowed}>
						Move here
					</Button>
				</>
			}
		>
			<nav aria-label="Folders" className="flex flex-wrap items-center gap-1 text-sm text-muted-foreground">
				{trail.map((f, i) => (
					<span key={f.id} className="flex items-center gap-1">
						{i > 0 && <ChevronRight aria-hidden className="size-3.5" />}
						<button type="button" onClick={() => setTrail(trail.slice(0, i + 1))} className="underline-offset-4 hover:underline">
							{f.name}
						</button>
					</span>
				))}
			</nav>
			<ul className="mt-3 max-h-72 overflow-auto rounded-md border border-border">
				{listing.isPending ? (
					<li className="px-3 py-2 text-sm text-muted-foreground">Loading…</li>
				) : folders.length === 0 ? (
					<li className="px-3 py-2 text-sm text-muted-foreground">No folders in here.</li>
				) : (
					folders.map((f) => (
						<li key={f.id}>
							<button
								type="button"
								onClick={() => setTrail([...trail, { id: f.id, name: f.name }])}
								className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-muted"
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
