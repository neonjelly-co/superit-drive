import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, api, type Existing, type Item, type Listing, RESUMABLE_FROM, type Session } from "./api";
import { t } from "./i18n";
import { composed, freeName, nameProblem } from "./names";

/** One file (or empty folder) the person chose or dropped, and how its upload is going. */
export type Upload = {
	key: number;
	/** The name it is stored under: the file's own, or the one the Drive gave it when both were kept. */
	name: string;
	kind: "file" | "folder";
	/** For a folder uploaded whole: the folders it goes into below where it was dropped, "Photos › 2026". */
	where?: string;
	size: number;
	state: "waiting" | "asking" | "uploading" | "reconnecting" | "done" | "failed" | "skipped" | "cancelled";
	/** Bytes sent so far. */
	sent: number;
	/** While waiting: true when the Drive is busy with other transfers and this one waits its turn there. */
	turn?: boolean;
	/** Why it was not uploaded, written for the person. */
	message?: string;
	/** Something the person should know about it that is not a failure, written for them. */
	note?: string;
	/** What it did with a name that was taken. */
	existing?: Existing;
};

const going = new Set<Upload["state"]>(["waiting", "asking", "uploading", "reconnecting"]);

/** Whether an upload is still on its way. */
export const isGoing = (u: Upload) => going.has(u.state);

/**
 * Something dropped or chosen: a file, with the folders it is in below where it goes when a whole folder was chosen
 * or dropped; or, without a file, such a folder that is empty.
 */
export type Chosen = { file?: File; folders: string[] };

/** The question an upload of a taken name waits on: replace or keep both. */
export type Question = {
	key: number;
	name: string;
	/** What has the name: a file can be replaced, a folder only kept beside. */
	taken: "file" | "folder";
	/** How many other uploads waiting in this batch have a taken name too. */
	others: number;
};

export type Answer = Existing | "skip";

type Job = {
	key: number;
	/** None for an empty folder: only the folders are made. */
	file?: File;
	name: string;
	/** The folders it goes into, below folderId, made when missing. */
	folders: string[];
	placeId: number;
	folderId: number | null;
	/** What has the name where it goes, as far as the list showed it or the Drive answered. */
	taken?: "file" | "folder";
	existing?: Existing;
	/** Keeps its place in line at the Drive while it is not its turn. */
	turn: string;
	stop: AbortController;
};

let nextKey = 1;

const randomTurn = () => Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => b.toString(16).padStart(2, "0")).join("");

const isAbort = (e: unknown) => e instanceof DOMException && e.name === "AbortError";

/** Waits ms, or until signal says stop (then it rejects). */
function sleep(ms: number, signal: AbortSignal): Promise<void> {
	return new Promise((resolve, reject) => {
		if (signal.aborted) {
			reject(new DOMException("Cancelled.", "AbortError"));
			return;
		}
		const timer = setTimeout(resolve, ms);
		signal.addEventListener(
			"abort",
			() => {
				clearTimeout(timer);
				reject(new DOMException("Cancelled.", "AbortError"));
			},
			{ once: true },
		);
	});
}

/** Whether an error is one to try again after a pause: the connection broke, or the Drive could not answer now. */
const passing = (e: unknown) => e instanceof ApiError && (e.status === 0 || (e.status >= 500 && e.status !== 507));

/**
 * Uploads files, one at a time, into the folder that was being looked at when they were chosen, and keeps its list in
 * step: each file shows in the list as soon as the Drive has it. A whole folder (chosen or dropped) is made there with
 * the folders in it, under a name of its own when its name is taken, so nothing is mixed into a folder that is there.
 * A name that is already there is not sent until the person answers "replace or keep both" (or skips it); one too long
 * is refused here without sending anything.
 *
 * Large files go in parts, and a connection that drops goes on from where the Drive says the file stands; where the
 * storage cannot do that, the file starts again and the person is told. When the Drive is busy with other transfers,
 * an upload waits its turn and shows so.
 */
export function useUploads(placeId: number | undefined, folderId: number | null, items: Item[] | undefined) {
	const queryClient = useQueryClient();
	const [uploads, setUploads] = useState<Upload[]>([]);
	const [question, setQuestion] = useState<Question | null>(null);
	const queue = useRef<Job[]>([]);
	const current = useRef<Job | null>(null);
	const running = useRef(false);
	const answered = useRef<((a: Answer) => void) | null>(null);
	/** The folders made for this page's uploads, by place, folder and path, so each is made once. */
	const made = useRef(new Map<string, Promise<number>>());

	const update = useCallback(
		(key: number, change: Partial<Upload>) =>
			setUploads((all) => all.map((u) => (u.key === key ? { ...u, ...change } : u))),
		[],
	);

	/** Asks about a taken name and waits for the answer. */
	const ask = useCallback(
		(job: Job): Promise<Answer> => {
			update(job.key, { state: "asking" });
			setQuestion({
				key: job.key,
				name: job.name,
				taken: job.taken ?? "file",
				others: queue.current.filter((j) => j.taken && !j.existing).length,
			});
			return new Promise((resolve) => {
				answered.current = resolve;
			});
		},
		[update],
	);

	/** What is in a folder changed: its list, and the space used. */
	const changed = useCallback(
		(place: number, folder: number | null) => {
			void queryClient.invalidateQueries({ queryKey: ["items", place, folder] });
			void queryClient.invalidateQueries({ queryKey: ["usage"] });
		},
		[queryClient],
	);

	/** The folder a job goes into: folderId, with its folders made below it where they are missing. */
	const folderOf = useCallback(
		async (job: Job): Promise<number | null> => {
			let parent = job.folderId;
			for (let i = 0; i < job.folders.length; i++) {
				const name = job.folders[i] as string;
				const key = `${job.placeId}|${job.folderId}|${job.folders.slice(0, i + 1).join("/")}`;
				let id = made.current.get(key);
				if (!id) {
					const into = parent;
					id = (async () => {
						const problem = nameProblem(name);
						if (problem) throw new ApiError(400, t.uploads.folderCannotBeMade(name, problem));
						try {
							const folder = await api.newFolder(job.placeId, into, name);
							changed(job.placeId, into);
							return folder.id;
						} catch (e) {
							// Made already, by this upload before the page was reloaded, or by someone else: go into it.
							if (!(e instanceof ApiError && e.status === 409 && e.exists === "folder")) throw e;
							const there = (await api.list(job.placeId, into)).items.find((f) => f.kind === "folder" && f.name === name);
							if (!there) throw e;
							return there.id;
						}
					})();
					made.current.set(key, id);
					id.catch(() => made.current.delete(key));
				}
				parent = await id;
			}
			return parent;
		},
		[changed],
	);

	/** Sends a file in one request, waiting its turn when the Drive is busy. */
	const sendWhole = useCallback(
		async (job: Job, file: File, into: number | null) => {
			for (let tries = 1; ; tries++) {
				update(job.key, { state: "uploading", sent: 0, turn: false, existing: job.existing });
				try {
					return await api.upload(job.placeId, into, file, job.name, job.existing, job.turn,
						(sent) => update(job.key, { sent }), job.stop.signal);
				} catch (e) {
					if (e instanceof ApiError && e.more.busy) {
						update(job.key, { state: "waiting", turn: true });
						await sleep((e.more.retryAfter ?? 2) * 1000, job.stop.signal);
						tries--;
						continue;
					}
					// The answer that it was not its turn can reach the browser as a broken connection: ask again.
					if (!passing(e) || tries >= 4) throw e;
					update(job.key, { state: "reconnecting" });
					await sleep(2000 * tries, job.stop.signal);
				}
			}
		},
		[update],
	);

	/**
	 * Sends a large file as a resumable upload: started, then in parts, then finished. When a part does not arrive, it
	 * asks the Drive where the upload stands and goes on from there, trying again for as long as the person stays.
	 */
	const sendResumable = useCallback(
		async (job: Job, file: File, into: number | null) => {
			const { placeId, key, stop } = job;
			let s: Session = await api.startUpload(placeId, into, job.name, file.size, job.existing);
			try {
				let offset = s.offset;
				let pause = 1000;
				while (offset < file.size) {
					const from = offset;
					const to = s.resumable ? Math.min(from + s.partSize, file.size) : file.size;
					let progressed = 0;
					const progress = (n: number) => {
						progressed = n;
						update(key, { sent: from + n });
					};
					update(key, { state: "uploading", sent: from, turn: false, existing: job.existing });
					try {
						offset = (await api.uploadPart(placeId, s.id, from, file.slice(from, to), progress, stop.signal)).offset;
						pause = 1000;
						continue;
					} catch (e) {
						if (isAbort(e)) throw e;
						if (e instanceof ApiError && e.more.busy) {
							update(key, { state: "waiting", turn: true });
							await sleep((e.more.retryAfter ?? 2) * 1000, stop.signal);
							continue;
						}
						if (e instanceof ApiError && e.status === 409 && e.more.offset !== undefined) {
							// It does not start where the file on the Drive ends: go on from there. Or the storage turned out
							// unable to resume: the whole file again.
							if (e.more.resumable === false) {
								s = { ...s, resumable: false, partSize: file.size };
								if (from > 0) update(key, { note: t.uploads.startedAgain });
							}
							offset = e.more.offset;
							continue;
						}
						// Another part still on its way at the Drive (409) settles soon; anything else the Drive refused stays.
						if (!passing(e) && !(e instanceof ApiError && e.status === 409)) throw e;
					}
					// The part did not arrive whole: once the connection is back, ask where the upload stands.
					update(key, { state: "reconnecting" });
					await sleep(pause, stop.signal);
					pause = Math.min(pause * 2, 30_000);
					try {
						const now = await api.uploadState(placeId, s.id);
						if (!now.resumable && now.offset === 0 && progressed > 0) update(key, { note: t.uploads.startedAgain });
						s = now;
						offset = now.offset;
						if (now.waiting) update(key, { state: "waiting", turn: true });
					} catch (e) {
						if (!passing(e)) throw e; // gone, or no longer theirs to upload into
					}
				}
				for (let tries = 1; ; tries++) {
					try {
						return await api.finishUpload(placeId, s.id, job.existing);
					} catch (e) {
						if (e instanceof ApiError && e.status === 409 && e.exists && !job.existing) {
							// Something of its name was put there meanwhile: ask, as before any upload.
							job.taken = e.exists;
							const a = await ask(job);
							if (a === "skip") throw new DOMException("Skipped.", "AbortError");
							job.existing = a;
							tries--;
							continue;
						}
						if (!passing(e) || tries >= 6) throw e;
						await sleep(1000 * tries, stop.signal);
					}
				}
			} catch (e) {
				if (isAbort(e)) void api.cancelUpload(placeId, s.id).catch(() => undefined);
				throw e;
			}
		},
		[ask, update],
	);

	const run = useCallback(async () => {
		if (running.current) return;
		running.current = true;
		for (let job = queue.current.shift(); job; job = queue.current.shift()) {
			current.current = job;
			const { key, file, placeId } = job;
			try {
				const into = await folderOf(job);
				if (!file) {
					update(key, { state: "done" });
					continue;
				}
				for (;;) {
					if (job.taken && !job.existing) {
						const a = await ask(job);
						if (a === "skip") {
							update(key, { state: "skipped" });
							break;
						}
						job.existing = a;
					}
					try {
						const item = file.size > RESUMABLE_FROM ? await sendResumable(job, file, into) : await sendWhole(job, file, into);
						update(key, { state: "done", sent: file.size, name: item.name });
						// Show it at once, then ask the Drive for the list as it now is.
						queryClient.setQueryData<Listing>(["items", placeId, into], (old) =>
							old ? { ...old, items: [...old.items.filter((i) => i.id !== item.id && i.name !== item.name), item] } : old,
						);
						changed(placeId, into);
						if (item.replaced) void queryClient.invalidateQueries({ queryKey: ["trash", placeId] });
					} catch (e) {
						if (e instanceof ApiError && e.status === 409 && e.exists && !job.existing) {
							job.taken = e.exists; // taken since the list was read: ask now
							continue;
						}
						throw e;
					}
					break;
				}
			} catch (e) {
				if (job.stop.signal.aborted) update(key, { state: "cancelled", turn: false });
				else if (isAbort(e)) update(key, { state: "skipped", turn: false });
				else update(key, { state: "failed", turn: false, message: e instanceof Error ? e.message : String(e) });
			}
		}
		current.current = null;
		running.current = false;
	}, [ask, changed, folderOf, queryClient, sendResumable, sendWhole, update]);

	/** Answers the question; with forOthers, the same answer goes to the other uploads of a taken name waiting. */
	const answer = useCallback((a: Answer, forOthers: boolean) => {
		if (forOthers) {
			for (const j of queue.current) {
				if (!j.taken || j.existing) continue;
				if (a === "skip") {
					j.existing = undefined;
					j.taken = undefined;
					queue.current = queue.current.filter((q) => q !== j);
					setUploads((all) => all.map((u) => (u.key === j.key ? { ...u, state: "skipped" } : u)));
				} else if (a === "keep-both" || j.taken === "file") {
					j.existing = a; // a file never replaces a folder: those are still asked
				}
			}
		}
		setQuestion(null);
		const resolve = answered.current;
		answered.current = null;
		resolve?.(a);
	}, []);

	const add = useCallback(
		(chosen: Chosen[]) => {
			if (placeId === undefined || chosen.length === 0) return;
			// A folder chosen or dropped whole goes in under its own name, or under a name of its own when that is taken.
			const taken: { name: string }[] = [...(items ?? [])];
			const renamed = new Map<string, string>();
			for (const c of chosen) {
				const top = c.folders[0];
				if (top === undefined || renamed.has(top)) continue;
				const name = freeName(composed(top), taken, true);
				renamed.set(top, name);
				taken.push({ name });
			}
			const added: Upload[] = [];
			for (const c of chosen) {
				const key = nextKey++;
				const folders = c.folders.map((f, i) => (i === 0 ? (renamed.get(f) ?? f) : composed(f)));
				const where = folders.length > 0 ? folders.join(" › ") : undefined;
				const stop = new AbortController();
				if (!c.file) {
					// An empty folder: its own row, so a folder that cannot be made is not missed.
					const name = folders.at(-1) ?? "";
					added.push({ key, name, kind: "folder", where: folders.slice(0, -1).join(" › ") || undefined, size: 0, state: "waiting", sent: 0 });
					queue.current.push({ key, name, folders, placeId, folderId, turn: randomTurn(), stop });
					continue;
				}
				const name = composed(c.file.name);
				const problem = nameProblem(name);
				added.push({ key, name, kind: "file", where, size: c.file.size, state: problem ? "failed" : "waiting", sent: 0, message: problem });
				if (problem) continue;
				const there = folders.length === 0 ? items?.find((i) => i.name === name) : undefined;
				queue.current.push({ key, file: c.file, name, folders, placeId, folderId, taken: there?.kind, turn: randomTurn(), stop });
			}
			setUploads((all) => [...all, ...added]);
			void run();
		},
		[placeId, folderId, items, run],
	);

	/** Stops an upload: one still waiting never starts; one on its way stops, and what arrived of it is removed. */
	const cancel = useCallback(
		(key: number) => {
			const waiting = queue.current.find((j) => j.key === key);
			if (waiting) {
				queue.current = queue.current.filter((j) => j !== waiting);
				update(key, { state: "cancelled", turn: false });
				return;
			}
			if (current.current?.key === key) current.current.stop.abort();
		},
		[update],
	);

	const active = uploads.some(isGoing);

	// Leaving the page stops what is still on its way; the browser asks first.
	useEffect(() => {
		if (!active) return;
		const warn = (e: BeforeUnloadEvent) => e.preventDefault();
		window.addEventListener("beforeunload", warn);
		return () => window.removeEventListener("beforeunload", warn);
	}, [active]);

	/** Forgets the finished uploads. */
	const clear = useCallback(() => setUploads((all) => all.filter(isGoing)), []);

	return { uploads, add, cancel, clear, active, question, answer };
}

/**
 * What was dropped: files, and folders with everything in them. Call it during the drop event: the browser forgets
 * what was dropped once the event is over, and only the reading of the folders goes on after. Browsers tell files and
 * folders apart through webkitGetAsEntry, which every current one has.
 */
export function droppedFiles(data: DataTransfer): Promise<Chosen[]> {
	const entries: FileSystemEntry[] = [];
	const plain: File[] = [];
	for (const item of Array.from(data.items)) {
		if (item.kind !== "file") continue;
		const entry = item.webkitGetAsEntry?.();
		if (entry) {
			entries.push(entry);
			continue;
		}
		const file = item.getAsFile();
		if (file) plain.push(file);
	}
	return (async () => {
		const out: Chosen[] = plain.map((file) => ({ file, folders: [] }));
		for (const e of entries) await walk(e, [], out);
		return out;
	})();
}

/** Adds what entry is to out: a file, or a folder with everything in it (itself, when it is empty). */
async function walk(entry: FileSystemEntry, folders: string[], out: Chosen[]): Promise<void> {
	if (entry.isFile) {
		const file = await new Promise<File>((resolve, reject) => (entry as FileSystemFileEntry).file(resolve, reject));
		out.push({ file, folders });
		return;
	}
	if (!entry.isDirectory) return;
	const path = [...folders, entry.name];
	const reader = (entry as FileSystemDirectoryEntry).createReader();
	let empty = true;
	for (;;) {
		// readEntries gives a folder's entries a batch at a time, and an empty batch at the end.
		const batch = await new Promise<FileSystemEntry[]>((resolve, reject) => reader.readEntries(resolve, reject));
		if (batch.length === 0) break;
		empty = false;
		for (const e of batch) await walk(e, path, out);
	}
	if (empty) out.push({ folders: path });
}

/** The files of a folder chosen whole (an input with webkitdirectory), each with the folders it is in. */
export function chosenFolder(files: File[]): Chosen[] {
	return files.map((file) => ({ file, folders: file.webkitRelativePath.split("/").slice(0, -1) }));
}
