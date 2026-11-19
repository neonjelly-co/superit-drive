import { useCallback, useEffect, useState } from "react";

/**
 * Where the person is: a folder of a place (place null for their "My files", folder null for the place's top folder),
 * "Shared with me", or the trash. It lives in the address (`/?folder=12`, `/?place=5&folder=12`, `/?shared`,
 * `/?trash`), so reloading keeps it and the browser's Back goes up again.
 */
export type Where =
	| { view: "files"; place: number | null; folder: number | null }
	| { view: "shared" }
	| { view: "trash" };

const id = (v: string | null) => {
	const n = Number(v);
	return Number.isSafeInteger(n) && n > 0 ? n : null;
};

function read(): Where {
	const q = new URLSearchParams(window.location.search);
	if (q.has("trash")) return { view: "trash" };
	if (q.has("shared")) return { view: "shared" };
	return { view: "files", place: id(q.get("place")), folder: id(q.get("folder")) };
}

function address(w: Where): string {
	if (w.view === "trash") return "/?trash";
	if (w.view === "shared") return "/?shared";
	const q = new URLSearchParams();
	if (w.place !== null) q.set("place", String(w.place));
	if (w.folder !== null) q.set("folder", String(w.folder));
	const s = q.toString();
	return s ? `/?${s}` : "/";
}

export function useWhere(): [Where, (w: Where) => void] {
	const [where, setWhere] = useState(read);
	useEffect(() => {
		const onPop = () => setWhere(read());
		window.addEventListener("popstate", onPop);
		return () => window.removeEventListener("popstate", onPop);
	}, []);
	const go = useCallback((w: Where) => {
		const next = address(w);
		if (next !== window.location.pathname + window.location.search) window.history.pushState(null, "", next);
		setWhere(w);
	}, []);
	return [where, go];
}

/** The link for a place to go, for an <a href>; clicks are handled by the page itself. */
export const hrefOf = address;
