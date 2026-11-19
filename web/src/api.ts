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

/** A folder in a path, such as the breadcrumb. */
export type FolderRef = { id: number; name: string };

/** What is in a folder, and the folders from the top of the place down to it (empty for the top folder). */
export type Listing = { items: Item[]; path: FolderRef[] };

/** An item in the trash. */
export type TrashItem = {
	id: number;
	name: string;
	kind: "folder" | "file";
	/** A file's size, or what the files in a folder add up to. */
	size: number;
	/** When it went to the trash, and when the Drive removes it for good (ISO 8601). */
	deleted: string;
	removed: string;
	/** True when an upload replaced it, rather than a person deleting it. */
	replaced?: boolean;
	/** The folder it goes back to when restored; empty for the top folder. */
	from: FolderRef[];
};

/** About how much space the Drive's files take, and the storage's size when it is known. Bytes. */
export type Usage = { used: number; capacity?: number };

/** What an upload does when its name is taken: replace the file there, or keep both. */
export type Existing = "replace" | "keep-both";

/** An answer the Drive gave with an error. message is written for the person. */
export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string,
		/** For a name that is taken (409): what kind of item has it. */
		readonly exists?: "file" | "folder",
	) {
		super(message);
	}
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
	let res: Response;
	try {
		res = await fetch(path, {
			method,
			headers: body === undefined ? { Accept: "application/json" } : { Accept: "application/json", "Content-Type": "application/json" },
			body: body === undefined ? undefined : JSON.stringify(body),
		});
	} catch {
		throw new ApiError(0, "The Drive could not be reached. Check your connection and try again.");
	}
	const data = await res.json().catch(() => null);
	if (!res.ok) {
		throw new ApiError(res.status, data?.error ?? `The Drive answered with an error (${res.status}).`, data?.exists);
	}
	return data as T;
}

/** The address of a folder of a place: the place itself for its top folder (null). */
const folderPath = (placeId: number, folderId: number | null) =>
	folderId === null ? `/api/places/${placeId}` : `/api/places/${placeId}/items/${folderId}`;

/**
 * Uploads one file into a folder of a place: its bytes as the request's body, its name in the address. The Drive
 * stores the whole file or nothing. When the name is taken it refuses with an ApiError whose `exists` says by what,
 * unless `existing` says to replace the file there or keep both. XMLHttpRequest rather than fetch, because only it
 * reports how much has been sent.
 */
function upload(
	placeId: number,
	folderId: number | null,
	file: File,
	name: string,
	existing: Existing | undefined,
	onProgress: (sent: number) => void,
): Promise<Item & { replaced?: boolean }> {
	const query = new URLSearchParams({ name });
	if (existing) query.set("existing", existing);
	const path = folderId === null ? `/api/places/${placeId}/files` : `/api/places/${placeId}/items/${folderId}/files`;
	return new Promise((resolve, reject) => {
		const xhr = new XMLHttpRequest();
		xhr.open("POST", `${path}?${query}`);
		xhr.setRequestHeader("Accept", "application/json");
		xhr.setRequestHeader("Content-Type", "application/octet-stream");
		xhr.responseType = "json";
		xhr.upload.onprogress = (e) => onProgress(e.loaded);
		xhr.onload = () => {
			const body = xhr.response as (Item & { replaced?: boolean; error?: string; exists?: "file" | "folder" }) | null;
			if (xhr.status === 201 && body) resolve(body);
			else reject(new ApiError(xhr.status, body?.error ?? `The Drive answered with an error (${xhr.status}).`, body?.exists));
		};
		xhr.onerror = () =>
			reject(new ApiError(0, "The upload could not reach the Drive. Check your connection and try again."));
		xhr.send(file);
	});
}

export const api = {
	me: () => call<Me>("GET", "/api/me"),
	places: () => call<{ places: Place[] }>("GET", "/api/places").then((r) => r.places),
	usage: () => call<Usage>("GET", "/api/usage"),
	/** What is in a folder of a place (null: its top folder). */
	list: (placeId: number, folderId: number | null) => call<Listing>("GET", `${folderPath(placeId, folderId)}/items`),
	newFolder: (placeId: number, folderId: number | null, name: string) =>
		call<Item>("POST", `${folderPath(placeId, folderId)}/folders`, { name }),
	rename: (placeId: number, itemId: number, name: string) =>
		call<Item>("POST", `/api/places/${placeId}/items/${itemId}/rename`, { name }),
	/** Moves an item into a folder of the same place (null: its top folder). */
	move: (placeId: number, itemId: number, to: number | null) =>
		call<Item>("POST", `/api/places/${placeId}/items/${itemId}/move`, { to }),
	/** Moves an item into the place's trash. */
	remove: (placeId: number, itemId: number) => call<TrashItem>("DELETE", `/api/places/${placeId}/items/${itemId}`),
	trash: (placeId: number) => call<{ items: TrashItem[] }>("GET", `/api/places/${placeId}/trash`).then((r) => r.items),
	restore: (placeId: number, itemId: number) =>
		call<{ item: Item; path: FolderRef[] }>("POST", `/api/places/${placeId}/trash/${itemId}/restore`),
	upload,
	/** The address that downloads a file under its original name. */
	downloadUrl: (placeId: number, itemId: number) => `/api/places/${placeId}/items/${itemId}/download`,
};
