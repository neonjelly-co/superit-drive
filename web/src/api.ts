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

/**
 * Uploads one file into the top folder of a place: its bytes as the request's body, its name in the address. The
 * Drive stores the whole file or nothing, and never replaces a file of the same name. XMLHttpRequest rather than
 * fetch, because only it reports how much has been sent.
 */
function upload(placeId: number, file: File, onProgress: (sent: number) => void): Promise<Item> {
	return new Promise((resolve, reject) => {
		const xhr = new XMLHttpRequest();
		xhr.open("POST", `/api/places/${placeId}/files?name=${encodeURIComponent(file.name)}`);
		xhr.setRequestHeader("Accept", "application/json");
		xhr.setRequestHeader("Content-Type", "application/octet-stream");
		xhr.responseType = "json";
		xhr.upload.onprogress = (e) => onProgress(e.loaded);
		xhr.onload = () => {
			const body = xhr.response as (Item & { error?: string }) | null;
			if (xhr.status === 201 && body) resolve(body);
			else reject(new ApiError(xhr.status, body?.error ?? `The Drive answered with an error (${xhr.status}).`));
		};
		xhr.onerror = () =>
			reject(new ApiError(0, "The upload could not reach the Drive. Check your connection and try again."));
		xhr.send(file);
	});
}

export const api = {
	me: () => get<Me>("/api/me"),
	places: () => get<{ places: Place[] }>("/api/places").then((r) => r.places),
	items: (placeId: number) => get<{ items: Item[] }>(`/api/places/${placeId}/items`).then((r) => r.items),
	upload,
	/** The address that downloads a file under its original name. */
	downloadUrl: (placeId: number, itemId: number) => `/api/places/${placeId}/items/${itemId}/download`,
};
