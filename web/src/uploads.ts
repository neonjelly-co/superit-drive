import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, type Item } from "./api";
import { uploadProblem } from "./names";

/** One file the person chose or dropped, and how its upload is going. */
export type Upload = {
	key: number;
	name: string;
	size: number;
	state: "waiting" | "uploading" | "done" | "failed";
	/** Bytes sent so far. */
	sent: number;
	/** Why it was not uploaded, written for the person. */
	message?: string;
};

/** Something dropped or chosen: a file, or a folder the Drive cannot take yet. */
export type Chosen = { file: File } | { folder: string };

const FOLDER =
	"This is a folder, and folders cannot be uploaded yet. Open it and choose the files inside instead.";

let nextKey = 1;

/**
 * Uploads files into the top folder of a place, one at a time, and keeps the list in step: each file shows in the list
 * as soon as the Drive has it. Names that would be refused anyway (too long, or already in the list) are refused
 * here without sending anything.
 */
export function useUploads(placeId: number | undefined, items: Item[] | undefined) {
	const queryClient = useQueryClient();
	const [uploads, setUploads] = useState<Upload[]>([]);
	const queue = useRef<{ key: number; file: File; placeId: number }[]>([]);
	const running = useRef(false);

	const update = useCallback(
		(key: number, change: Partial<Upload>) =>
			setUploads((all) => all.map((u) => (u.key === key ? { ...u, ...change } : u))),
		[],
	);

	const run = useCallback(async () => {
		if (running.current) return;
		running.current = true;
		for (let next = queue.current.shift(); next; next = queue.current.shift()) {
			const { key, file, placeId } = next;
			update(key, { state: "uploading" });
			try {
				const item = await api.upload(placeId, file, (sent) => update(key, { sent }));
				update(key, { state: "done", sent: file.size });
				// Show it at once, then ask the Drive for the list as it now is.
				queryClient.setQueryData<Item[]>(["items", placeId], (old) =>
					old ? [...old.filter((i) => i.id !== item.id && i.name !== item.name), item] : old,
				);
				void queryClient.invalidateQueries({ queryKey: ["items", placeId] });
			} catch (e) {
				update(key, { state: "failed", message: e instanceof Error ? e.message : String(e) });
			}
		}
		running.current = false;
	}, [queryClient, update]);

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
				const problem = uploadProblem(c.file.name, items);
				added.push({
					key,
					name: c.file.name,
					size: c.file.size,
					state: problem ? "failed" : "waiting",
					sent: 0,
					message: problem,
				});
				if (!problem) queue.current.push({ key, file: c.file, placeId });
			}
			setUploads((all) => [...all, ...added]);
			void run();
		},
		[placeId, items, run],
	);

	const active = uploads.some((u) => u.state === "waiting" || u.state === "uploading");

	// Leaving the page stops what is still on its way; the browser asks first.
	useEffect(() => {
		if (!active) return;
		const warn = (e: BeforeUnloadEvent) => e.preventDefault();
		window.addEventListener("beforeunload", warn);
		return () => window.removeEventListener("beforeunload", warn);
	}, [active]);

	/** Forgets the finished uploads. */
	const clear = useCallback(
		() => setUploads((all) => all.filter((u) => u.state === "waiting" || u.state === "uploading")),
		[],
	);

	return { uploads, add, clear, active };
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
