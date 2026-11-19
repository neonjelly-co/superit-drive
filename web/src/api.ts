// The Drive's JSON API, as the interface uses it. The server side is internal/server/routes.go.

export type Me = {
	userId: string;
	email: string;
	displayName: string;
	role: "admin" | "member";
	project: string;
};

/**
 * What the Drive may let a person do in a place or with an item (internal/access). It decides again on every request.
 */
export type Action =
	| "list"
	| "download"
	| "upload"
	| "make-folder"
	| "rename"
	| "move"
	| "delete"
	| "see-trash"
	| "restore"
	| "see-access"
	| "share"
	| "manage-access"
	| "delete-shared-folder"
	| "restore-shared-folder";

/**
 * A role that can be given: viewer or editor on a file or folder, and also manager on a shared folder as a whole.
 * Project admins are managers of every shared folder.
 */
export type Role = "viewer" | "editor" | "manager";

/** Someone's role at an item: what was given, or "owner" in their own My files. */
export type RoleAt = Role | "owner";

/**
 * A place the person can open: their own "My files", or a shared folder (owned by the project) with their role on it.
 * `can` is what they may do there.
 */
export type Place =
	| { id: number; kind: "my-files"; can: Action[] }
	| { id: number; kind: "shared-folder"; name: string; role: Role; can: Action[] };

/** A shared folder in the trash. */
export type DeletedPlace = {
	id: number;
	kind: "shared-folder";
	name: string;
	/** What the files in it add up to, its own trash included. */
	size: number;
	/** When it went to the trash, and when the Drive removes it for good (ISO 8601). */
	deleted: string;
	removed: string;
};

/** A member of the project, from the member list. */
export type Member = { userId: string; email: string; displayName: string; role: "admin" | "member" };

/** Who can see an item, for the list's sentence: everyone in the project, or these members (by user id). */
export type SeenBy = { everyone: boolean; members: string[] };

/** One line of "who can see this", for an item or a shared folder as a whole. */
export type Access = {
	/** Everyone in the project; otherwise one member. */
	everyone?: boolean;
	userId?: string;
	email?: string;
	displayName?: string;
	role: RoleAt;
	/** Whose My files it is, or a project admin (a manager of every shared folder): neither given nor taken away. */
	owner?: boolean;
	admin?: boolean;
	/** Given right here, so whoever may share it can change it or take it away here. */
	given: boolean;
	/** Given on a folder above, or on the shared folder as a whole: `from` is that folder when the person can open it. */
	inherited?: boolean;
	from?: FolderRef;
	/** Given to someone no longer in the project. */
	former?: boolean;
};

/** Who access is given to: everyone in the project, or one member. */
export type Grantee = { everyone: true } | { userId: string };

export type Item = {
	id: number;
	name: string;
	kind: "folder" | "file";
	/** Bytes; files only. */
	size?: number;
	/** When the storage last saw it change (ISO 8601). */
	changed?: string;
	/** In a listing: what the person may do with it, and who can see it. */
	can?: Action[];
	seenBy?: SeenBy;
};

/** A folder in a path, such as the breadcrumb. */
export type FolderRef = { id: number; name: string };

/**
 * What is in a folder, and the folders down to it that the person can open (empty for the top folder): from the place's
 * top folder when `fromTop`, otherwise from the folder they were given. `role` and `can` are theirs in this folder.
 */
export type Listing = { items: Item[]; path: FolderRef[]; fromTop: boolean; role: RoleAt | ""; can: Action[] };

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

const accessPath = (placeId: number, itemId: number | null) =>
	itemId === null ? `/api/places/${placeId}/access` : `/api/places/${placeId}/items/${itemId}/access`;

const granteePath = (placeId: number, itemId: number | null, to: Grantee) =>
	"everyone" in to
		? `${accessPath(placeId, itemId)}/everyone`
		: `${accessPath(placeId, itemId)}/members/${encodeURIComponent(to.userId)}`;

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
	members: () => call<{ members: Member[] }>("GET", "/api/members").then((r) => r.members),
	/** My files first, then every shared folder the person has a role on. */
	places: () => call<{ places: Place[] }>("GET", "/api/places").then((r) => r.places),
	/** Makes a shared folder; the person becomes its manager. */
	newSharedFolder: (name: string) => call<Place>("POST", "/api/places", { name }),
	/** Moves a shared folder, with everything in it, into the trash. */
	deleteSharedFolder: (placeId: number) => call<DeletedPlace>("DELETE", `/api/places/${placeId}`),
	/** The shared folders in the trash that the person may restore. */
	deletedSharedFolders: () => call<{ places: DeletedPlace[] }>("GET", "/api/places/deleted").then((r) => r.places),
	restoreSharedFolder: (placeId: number) => call<Place>("POST", `/api/places/${placeId}/restore`),
	/** Who can see an item (or, with itemId null, a shared folder as a whole). */
	access: (placeId: number, itemId: number | null) =>
		call<{ access: Access[] }>("GET", accessPath(placeId, itemId)).then((r) => r.access),
	/** Gives a role on an item (or a shared folder as a whole), in place of what they had there. */
	giveAccess: (placeId: number, itemId: number | null, to: Grantee, role: Role) =>
		call<{ access: Access[] }>("PUT", granteePath(placeId, itemId, to), { role }).then((r) => r.access),
	/** Takes away what was given right there; access from a folder above stays. */
	takeAccess: (placeId: number, itemId: number | null, from: Grantee) =>
		call<{ access: Access[] }>("DELETE", granteePath(placeId, itemId, from)).then((r) => r.access),
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
