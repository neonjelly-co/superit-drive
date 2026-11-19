import { useCallback, useEffect, useState } from "react";

/**
 * Where the person is: a folder of "My files" (null for its top folder), or the trash. It lives in the address
 * (`/?folder=12`, `/?trash`), so reloading keeps it and the browser's Back goes up again.
 */
export type Where = { view: "files"; folder: number | null } | { view: "trash" };

function read(): Where {
	const q = new URLSearchParams(window.location.search);
	if (q.has("trash")) return { view: "trash" };
	const folder = Number(q.get("folder"));
	return { view: "files", folder: Number.isSafeInteger(folder) && folder > 0 ? folder : null };
}

function address(w: Where): string {
	if (w.view === "trash") return "/?trash";
	return w.folder === null ? "/" : `/?folder=${w.folder}`;
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
