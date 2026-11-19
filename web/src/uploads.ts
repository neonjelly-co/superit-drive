import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, api, type Existing, type Item, type Listing } from "./api";
import { composed, nameProblem } from "./names";

/** One file the person chose or dropped, and how its upload is going. */
export type Upload = {
	key: number;
	/** The name it is stored under: the file's own, or the one the Drive gave it when both were kept. */
	name: string;
	size: number;
	state: "waiting" | "asking" | "uploading" | "done" | "failed" | "skipped";
	/** Bytes sent so far. */
	sent: number;
	/** Why it was not uploaded, written for the person. */
	message?: string;
	/** What it did with a name that was taken. */
	existing?: Existing;
};

/** Something dropped or chosen: a file, or a folder the Drive cannot take yet. */
export type Chosen = { file: File } | { folder: string };

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

const FOLDER =
	"This is a folder, and folders cannot be uploaded yet. Open it and choose the files inside instead.";

type Job = {
	key: number;
	file: File;
	name: string;
	placeId: number;
	folderId: number | null;
	/** What has the name where it goes, as far as the list showed it or the Drive answered. */
	taken?: "file" | "folder";
	existing?: Existing;
};

let nextKey = 1;

/**
 * Uploads files, one at a time, into the folder that was being looked at when they were chosen, and keeps its list in
 * step: each file shows in the list as soon as the Drive has it. A name that is already there is not sent until the
 * person answers "replace or keep both" (or skips it); one too long is refused here without sending anything.
 */
export function useUploads(placeId: number | undefined, folderId: number | null, items: Item[] | undefined) {
	const queryClient = useQueryClient();
	const [uploads, setUploads] = useState<Upload[]>([]);
	const [question, setQuestion] = useState<Question | null>(null);
	const queue = useRef<Job[]>([]);
	const running = useRef(false);
	const answered = useRef<((a: Answer) => void) | null>(null);

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

	const run = useCallback(async () => {
		if (running.current) return;
		running.current = true;
		for (let job = queue.current.shift(); job; job = queue.current.shift()) {
			const { key, file, placeId, folderId } = job;
			for (;;) {
				if (job.taken && !job.existing) {
					const a = await ask(job);
					if (a === "skip") {
						update(key, { state: "skipped" });
						break;
					}
					job.existing = a;
				}
				update(key, { state: "uploading", sent: 0, existing: job.existing });
				try {
					const item = await api.upload(placeId, folderId, file, job.name, job.existing, (sent) => update(key, { sent }));
					update(key, { state: "done", sent: file.size, name: item.name });
					// Show it at once, then ask the Drive for the list as it now is.
					queryClient.setQueryData<Listing>(["items", placeId, folderId], (old) =>
						old ? { ...old, items: [...old.items.filter((i) => i.id !== item.id && i.name !== item.name), item] } : old,
					);
					void queryClient.invalidateQueries({ queryKey: ["items", placeId, folderId] });
					void queryClient.invalidateQueries({ queryKey: ["usage"] });
					if (item.replaced) void queryClient.invalidateQueries({ queryKey: ["trash", placeId] });
				} catch (e) {
					if (e instanceof ApiError && e.status === 409 && e.exists && !job.existing) {
						job.taken = e.exists; // taken since the list was read: ask now
						continue;
					}
					update(key, { state: "failed", message: e instanceof Error ? e.message : String(e) });
				}
				break;
			}
		}
		running.current = false;
	}, [ask, queryClient, update]);

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
			const added: Upload[] = [];
			for (const c of chosen) {
				const key = nextKey++;
				if ("folder" in c) {
					added.push({ key, name: c.folder, size: 0, state: "failed", sent: 0, message: FOLDER });
					continue;
				}
				const name = composed(c.file.name);
				const problem = nameProblem(name);
				added.push({ key, name, size: c.file.size, state: problem ? "failed" : "waiting", sent: 0, message: problem });
				if (problem) continue;
				const there = items?.find((i) => i.name === name);
				queue.current.push({ key, file: c.file, name, placeId, folderId, taken: there?.kind });
			}
			setUploads((all) => [...all, ...added]);
			void run();
		},
		[placeId, folderId, items, run],
	);

	const active = uploads.some((u) => u.state === "waiting" || u.state === "uploading" || u.state === "asking");

	// Leaving the page stops what is still on its way; the browser asks first.
	useEffect(() => {
		if (!active) return;
		const warn = (e: BeforeUnloadEvent) => e.preventDefault();
		window.addEventListener("beforeunload", warn);
		return () => window.removeEventListener("beforeunload", warn);
	}, [active]);

	/** Forgets the finished uploads. */
	const clear = useCallback(
		() => setUploads((all) => all.filter((u) => u.state === "waiting" || u.state === "uploading" || u.state === "asking")),
		[],
	);

	return { uploads, add, clear, active, question, answer };
}

/**
 * What was dropped: files, and the names of folders, which a drop also carries but which cannot be uploaded yet.
 * Browsers only tell the two apart through webkitGetAsEntry, which every current one has.
 */
export function droppedFiles(data: DataTransfer): Chosen[] {
	const out: Chosen[] = [];
	for (const item of Array.from(data.items)) {
		if (item.kind !== "file") continue;
		const entry = item.webkitGetAsEntry?.();
		if (entry?.isDirectory) {
			out.push({ folder: entry.name });
			continue;
		}
		const file = item.getAsFile();
		if (file) out.push({ file });
	}
	return out;
}
