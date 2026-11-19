// The Drive's JSON API, as the interface uses it. The server side is internal/server/routes.go.

export type Me = {
	userId: string;
	email: string;
	displayName: string;
	role: "admin" | "member";
	project: string;
};

export type Place = { id: number; kind: "my-files" };

export type Item = {
	id: number;
	name: string;
	kind: "folder" | "file";
	/** Bytes; files only. */
	size?: number;
	/** When the storage last saw it change (ISO 8601). */
	changed?: string;
};

/** An answer the Drive gave with an error. message is written for the person. */
export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string,
	) {
		super(message);
	}
}

async function get<T>(path: string): Promise<T> {
	let res: Response;
	try {
		res = await fetch(path, { headers: { Accept: "application/json" } });
	} catch {
		throw new ApiError(0, "The Drive could not be reached. Check your connection and try again.");
	}
	const body = await res.json().catch(() => null);
	if (!res.ok) {
		throw new ApiError(res.status, body?.error ?? `The Drive answered with an error (${res.status}).`);
	}
	return body as T;
}

export const api = {
	me: () => get<Me>("/api/me"),
	places: () => get<{ places: Place[] }>("/api/places").then((r) => r.places),
	items: (placeId: number) => get<{ items: Item[] }>(`/api/places/${placeId}/items`).then((r) => r.items),
};
