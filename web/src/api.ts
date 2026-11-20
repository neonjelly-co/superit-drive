// The Drive's JSON API, as the interface uses it. The server side is internal/server/routes.go.

/**
 * Where the API is: "/api" for members, signed in through the reverse proxy; the guests' own, under the public prefix,
 * for the guests' page (forGuests). A guest's routes are a member's under it, for what they may do.
 */
let base = "/api";

/** Makes every call go to the guests' API: the guests' page calls it once, before anything else. */
export function forGuests() {
	base = "/s/guest/api";
}

export type Me = {
	userId: string;
	email: string;
	displayName: string;
	role: "admin" | "member";
	project: string;
	/** There for seven days after the Drive storage was put back to a snapshot. */
	rollback?: Rollback;
	/** Whether guests can be invited on this Drive: not without mail that works, and why. */
	guests: { invite: boolean; why: string };
	/** Whether sharing outside the project (public links and guests) is on for the whole project. */
	sharingOutside: OutsideSwitch;
	/** What the person may do that concerns no place: a project admin oversees, and switches sharing outside. */
	can: ("enter" | "see-usage" | "make-shared-folder" | "oversee" | "switch-sharing-outside")[];
};

/**
 * A switch of sharing outside the project (public links and guests): on, or off since when (`at`) and by whom (`by`,
 * while they are a member; `former` once they have left).
 */
export type OutsideSwitch = { on: boolean; by?: Person; former?: boolean; at?: string };

/** Whether sharing outside the project is on in a place: "on", or switched off for the whole "project" or this "folder". */
export type Outside = "on" | "project" | "folder";

/** The Drive storage put back to a snapshot: who did it (text from the platform), and when (ISO 8601). */
export type Rollback = {
	by: string;
	/** When the snapshot was taken: the files are as they were then. */
	to: string;
	/** When it was put back; it also tells one rollback from another. */
	at: string;
	/** When the notice ends. */
	until: string;
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
	| "move-out"
	| "move-in"
	| "delete"
	| "see-trash"
	| "restore"
	| "see-access"
	| "share"
	| "make-link"
	| "manage-link"
	| "invite-guest"
	| "manage-access"
	| "switch-sharing-outside"
	| "delete-shared-folder"
	| "restore-shared-folder"
	| "hand-over-held"
	| "delete-held";

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
	| { id: number; kind: "my-files"; can: Action[]; outside: Outside }
	| {
			id: number;
			kind: "shared-folder";
			name: string;
			role: Role;
			can: Action[];
			/** Its own switch of sharing outside the project, which its managers switch. */
			sharingOutside: OutsideSwitch;
			/** Whether sharing outside the project is on in it now: off for it, or for the whole project. */
			outside: Outside;
	  };

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

/**
 * Who can see an item, for the list's sentence: everyone in the project, or these members (by user id); `link` when
 * anyone with a public link (on it, or on a folder above it) can; and how many guests can.
 */
export type SeenBy = { everyone: boolean; members: string[]; link?: boolean; guests?: number };

/** A guest: someone outside the project invited by email address. */
export type Guest = { id: number; address: string };

/** One line of "who can see this", for an item or a shared folder as a whole. */
export type Access = {
	/**
	 * Everyone in the project; `link`, anyone with the public link: through a view link (`linkKind` "view") as viewer;
	 * through an upload-only link on the item itself (`linkKind` "upload-only"), without a role: they send files in and
	 * see nothing. Otherwise one member.
	 */
	everyone?: boolean;
	link?: boolean;
	linkKind?: LinkKind;
	userId?: string;
	email?: string;
	displayName?: string;
	/** Every line has one but an upload-only link's: its visitors send files in, and see nothing. */
	role?: RoleAt;
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
	/** For a guest, invited by address. */
	guest?: Guest;
	/** When it ends, for access given with an end date (ISO 8601). */
	until?: string;
	/**
	 * For a guest, to whoever may share the item: whether it lets them in now, and how often they used it, and when
	 * last.
	 */
	works?: boolean;
	uses?: number;
	used?: string;
};

/** What typing an address in "Give access" came to: a member's address is shared with them, any other invites a guest. */
export type Invited = { access: Access[]; member?: Person; guest?: Guest; invited?: boolean };

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
	/** True for a file of a kind that opens in the browser (an ordinary image, a PDF, plain text, a video, a sound). */
	opens?: boolean;
	/**
	 * For members: who outside the project added it: a guest (uploaded or made), by their address then; or a visitor who
	 * sent it through an upload-only link (`link`).
	 */
	addedBy?: { guest?: string; link?: boolean };
};

/** A folder in a path, such as the breadcrumb. */
export type FolderRef = { id: number; name: string };

/**
 * What is in a folder, and the folders down to it that the person can open (empty for the top folder): from the place's
 * top folder when `fromTop`, otherwise from the folder they were given. `role` and `can` are theirs in this folder.
 * The list is the Drive's index at once; `checking` says the Drive is looking at the folder on the Drive storage in the
 * background, and asking again with `wait` answers once it has.
 */
export type Listing = {
	items: Item[];
	path: FolderRef[];
	fromTop: boolean;
	role: RoleAt | "";
	can: Action[];
	checking?: boolean;
	/** Whether sharing outside the project (public links and guests) is on here. */
	outside?: Outside;
};

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

/** A member as a list names them. */
export type Person = { userId: string; email: string; displayName: string };

/**
 * An item of "Shared with me": something shared with the person, by name or with everyone in the project, that they
 * reach only through that share. `place` is the place it is in, by id only (never a shared folder's name, which they
 * cannot open); `owner` is whose My files it is in. They open it at its own address: a folder at
 * `/?place=…&folder=…`, a file by its download address.
 */
export type SharedItem = Item & {
	place: number;
	placeKind: "my-files" | "shared-folder";
	owner?: Person;
	role: RoleAt;
	/** Who shared it with them, while they are in the project; toEveryone when it was shared with everyone. */
	sharedBy?: Person;
	toEveryone?: boolean;
	/** When it was shared (ISO 8601). */
	shared: string;
};

/**
 * A file or folder a search found, with where it is as the person may see it: `path` holds the folders above it that
 * they can open, from the place's top folder when `fromTop` (then a shared folder's name is `placeName`), and from the
 * highest folder they were given otherwise. `owner` is whose My files it is in, when it is not theirs.
 */
export type Found = Item & {
	place: number;
	placeKind: "my-files" | "shared-folder";
	placeName?: string;
	owner?: Person;
	role: RoleAt;
	path: FolderRef[];
	fromTop: boolean;
};

/**
 * Who would see an item where a move would put it, as "who can see this" would show it there, before it moves.
 * becomesProjects: it would leave the person's My files for a shared folder, and become the project's. outside: whether
 * sharing outside the project is on there (where it is off, its public link and guests stop there).
 */
export type MovePreview = { becomesProjects: boolean; access: Access[]; seenBy: SeenBy; outside: Outside };

/**
 * What a public link lets its visitors do: open and download what it is on ("view"), or, on a folder only, send files
 * into it and see nothing ("upload-only").
 */
export type LinkKind = "view" | "upload-only";

/**
 * The public link on a file or folder, as whoever may share it sees it. `address` is only in the answer that made it:
 * the Drive keeps a fingerprint of it, not the address, and cannot show it again. `works` is false while it lets nobody
 * in (whoever made it can no longer share the item, or sharing outside the project is off there). An upload-only link
 * also says how many bytes it has taken in since it was made or reset (`takenIn`), the most it takes (`limit`), and
 * whether it has (`full`): then it takes no file until it is reset.
 */
export type PublicLink = {
	kind: LinkKind;
	takenIn?: number;
	limit?: number;
	full?: boolean;
	made: string;
	madeBy?: Person;
	/** Whoever made it has left the project. */
	former?: boolean;
	uses: number;
	used?: string;
	works: boolean;
	address?: string;
};

/** About how much space the Drive's files take, and the storage's size when it is known. Bytes. */
export type Usage = { used: number; capacity?: number };

/**
 * One thing shared outside the project, as a project admin oversees it: a public link or a guest's invitation, on an
 * item by name (never what is in it, nor the folders above it), in a shared folder or someone's My files, who shared
 * it, with whom, since when, until when, how often it was used and when last, and whether it lets anyone in now.
 */
export type OutsideShare = {
	kind: "link" | "guest";
	item: { id: number; name: string; kind: "file" | "folder" };
	place: { id: number; kind: "my-files" | "shared-folder"; name?: string; owner?: Person; ownerLeft?: boolean; outside: Outside };
	sharedBy?: Person;
	former?: boolean;
	guest?: Guest;
	/** What it lets them do; an upload-only link has none (`linkKind`): its visitors send files in and see nothing. */
	role?: Role;
	linkKind?: LinkKind;
	since: string;
	until?: string;
	uses: number;
	used?: string;
	works: boolean;
};

/** The space one person's My files, or one shared folder, uses (its trash included). Bytes. */
export type SpaceUsed = {
	person?: Person;
	/** Someone who has left, with their files held: their top folder's name on the storage. */
	former?: boolean;
	folder?: string;
	id?: number;
	name?: string;
	deleted?: boolean;
	used: number;
};

/** The space each person and each shared folder uses, the largest first, and the whole. */
export type Space = { used: number; capacity?: number; people: SpaceUsed[]; sharedFolders: SpaceUsed[] };

/**
 * The My files of someone no longer in the member list, held: closed to everyone, with nothing shared from it working,
 * until a project admin hands it to another member or deletes it. Whose it was and how much it holds; never what is in it.
 */
export type Held = {
	/** The place's id, which handing it over or deleting it names. */
	place: number;
	userId: string;
	/** Their My files' folder on the storage: their email address when they first opened the Drive. */
	folder: string;
	/** Files and bytes outside its trash; `used` is all it takes, its trash included. */
	files: number;
	size: number;
	used: number;
	/** A hand-over or deletion under way; `back` when its owner is a member again meanwhile (it goes on). */
	act?: HeldAct;
	back?: boolean;
};

/** A hand-over or deletion of someone's held My files: who did it, when, and what. */
export type HeldAct = {
	id: number;
	kind: "hand-over" | "delete";
	userId: string;
	folder: string;
	files: number;
	size: number;
	/** The project admin who did it, while a member (`byFormer` once they have left). */
	by?: Person;
	byFormer?: boolean;
	at: string;
	/** For a hand-over: who got the files, and the folder they became in their My files. */
	to?: Person;
	toFormer?: boolean;
	into?: string;
	/** When the Drive finished it; none while it is under way. */
	done?: string;
};

/** What an upload does when its name is taken: replace the file there, or keep both. */
export type Existing = "replace" | "keep-both";

/** An answer the Drive gave with an error. message is written for the person. */
export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string,
		/** For a name that is taken (409): what kind of item has it. */
		readonly exists?: "file" | "folder",
		/** More the Drive said: for a transfer that is not its turn (503), busy and when to try again. */
		readonly more: {
			busy?: boolean;
			retryAfter?: number;
			/** For a part of an upload in progress that does not fit (409): where what has arrived ends. */
			offset?: number;
			/** False when the storage cannot resume, and the upload starts again from the beginning. */
			resumable?: boolean;
			/** For a guest: they are not signed in (401), and must sign in first. */
			signIn?: boolean;
			/** For a guest: what was given to them is not available now, as sharing outside the project is off (403). */
			unavailable?: boolean;
		} = {},
	) {
		super(message);
	}
}

/** An error the browser gave because the connection broke, not the Drive. */
const unreachable = (what: string) => new ApiError(0, `${what} could not reach the Drive. Check your connection.`);

/** A resumable upload in progress: started, its parts on their way, not yet finished. */
export type Session = {
	id: string;
	name: string;
	size: number;
	/** How much of the file has arrived: where the next part starts. */
	offset: number;
	/** Whether an upload cut off goes on from offset; when false, the file is sent as one part and starts again. */
	resumable: boolean;
	/** The most one part may hold. */
	partSize: number;
	/** True while it waits its turn for one of the Drive's transfer slots. */
	waiting: boolean;
};

/** Larger files are uploaded resumably, in parts; smaller ones in one request. */
export const RESUMABLE_FROM = 32 * 1024 * 1024;

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
		throw new ApiError(res.status, data?.error ?? `The Drive answered with an error (${res.status}).`, data?.exists, {
			busy: data?.busy,
			offset: data?.offset,
			resumable: data?.resumable,
			retryAfter: Number(res.headers.get("Retry-After")) || undefined,
			signIn: data?.signIn,
			unavailable: data?.unavailable,
		});
	}
	return data as T;
}

const accessPath = (placeId: number, itemId: number | null) =>
	itemId === null ? `${base}/places/${placeId}/access` : `${base}/places/${placeId}/items/${itemId}/access`;

const granteePath = (placeId: number, itemId: number | null, to: Grantee) =>
	"everyone" in to
		? `${accessPath(placeId, itemId)}/everyone`
		: `${accessPath(placeId, itemId)}/members/${encodeURIComponent(to.userId)}`;


type Uploaded = Item & { replaced?: boolean };

/**
 * Sends bytes with XMLHttpRequest rather than fetch, because only it reports how much has been sent. It resolves
 * with the answer when its status is `ok`, and rejects with an ApiError otherwise; abort stops it.
 */
function send<T>(
	method: string,
	path: string,
	body: Blob,
	ok: number,
	onProgress: (sent: number) => void,
	signal?: AbortSignal,
): Promise<T> {
	return new Promise((resolve, reject) => {
		const xhr = new XMLHttpRequest();
		xhr.open(method, path);
		xhr.setRequestHeader("Accept", "application/json");
		xhr.setRequestHeader("Content-Type", "application/octet-stream");
		xhr.responseType = "json";
		xhr.upload.onprogress = (e) => onProgress(e.loaded);
		xhr.onload = () => {
			const answer = xhr.response as
				| (T & { error?: string; exists?: "file" | "folder"; busy?: boolean; offset?: number; resumable?: boolean })
				| null;
			if (xhr.status === ok && answer) {
				resolve(answer);
				return;
			}
			const retryAfter = Number(xhr.getResponseHeader("Retry-After")) || undefined;
			reject(
				new ApiError(xhr.status, answer?.error ?? `The Drive answered with an error (${xhr.status}).`, answer?.exists, {
					busy: answer?.busy,
					retryAfter,
					offset: answer?.offset,
					resumable: answer?.resumable,
				}),
			);
		};
		xhr.onerror = () => reject(unreachable("The upload"));
		xhr.onabort = () => reject(new DOMException("The upload was cancelled.", "AbortError"));
		signal?.addEventListener("abort", () => xhr.abort(), { once: true });
		if (signal?.aborted) xhr.abort();
		else xhr.send(body);
	});
}

/** The address of a folder of a place: the place itself for its top folder (null). */
const inFolder = (placeId: number, folderId: number | null) =>
	folderId === null ? `${base}/places/${placeId}` : `${base}/places/${placeId}/items/${folderId}`;

/**
 * Uploads one file into a folder of a place in one request: its bytes as the request's body, its name in the address.
 * The Drive stores the whole file or nothing. When the name is taken it refuses with an ApiError whose `exists` says
 * by what, unless `existing` says to replace the file there or keep both. When it is not its turn for one of the
 * Drive's transfer slots, the ApiError says `busy`: send it again with the same `turn`, which keeps its place in line.
 */
function upload(
	placeId: number,
	folderId: number | null,
	file: File,
	name: string,
	existing: Existing | undefined,
	turn: string,
	onProgress: (sent: number) => void,
	signal?: AbortSignal,
): Promise<Uploaded> {
	const query = new URLSearchParams({ name, turn });
	if (existing) query.set("existing", existing);
	return send<Uploaded>("POST", `${inFolder(placeId, folderId)}/files?${query}`, file, 201, onProgress, signal);
}

const sessionPath = (placeId: number, id: string) => `${base}/places/${placeId}/uploads/${id}`;

export const api = {
	me: () => call<Me>("GET", `${base}/me`),
	members: () => call<{ members: Member[] }>("GET", `${base}/members`).then((r) => r.members),
	/** My files first, then every shared folder the person has a role on. */
	places: () => call<{ places: Place[] }>("GET", `${base}/places`).then((r) => r.places),
	/** Makes a shared folder; the person becomes its manager. */
	newSharedFolder: (name: string) => call<Place>("POST", `${base}/places`, { name }),
	/** Moves a shared folder, with everything in it, into the trash. */
	deleteSharedFolder: (placeId: number) => call<DeletedPlace>("DELETE", `${base}/places/${placeId}`),
	/** The shared folders in the trash that the person may restore. */
	deletedSharedFolders: () => call<{ places: DeletedPlace[] }>("GET", `${base}/places/deleted`).then((r) => r.places),
	restoreSharedFolder: (placeId: number) => call<Place>("POST", `${base}/places/${placeId}/restore`),
	/** Who can see an item (or, with itemId null, a shared folder as a whole). */
	access: (placeId: number, itemId: number | null) =>
		call<{ access: Access[] }>("GET", accessPath(placeId, itemId)).then((r) => r.access),
	/** Gives a role on an item (or a shared folder as a whole), in place of what they had there. */
	giveAccess: (placeId: number, itemId: number | null, to: Grantee, role: Role) =>
		call<{ access: Access[] }>("PUT", granteePath(placeId, itemId, to), { role }).then((r) => r.access),
	/** Takes away what was given right there; access from a folder above stays. */
	takeAccess: (placeId: number, itemId: number | null, from: Grantee) =>
		call<{ access: Access[] }>("DELETE", granteePath(placeId, itemId, from)).then((r) => r.access),
	/**
	 * Gives access to a file or folder to whoever has the address typed: a member's is shared with them; any other
	 * invites a guest by mail, until `ends` (ISO 8601) when given.
	 */
	invite: (placeId: number, itemId: number, address: string, role: Role, ends?: string) =>
		call<Invited>("POST", `${accessPath(placeId, itemId)}/guests`, { address, role, ends: ends ?? null }),
	/** Gives a guest's invitation a new role and end date (none: no end date). */
	changeInvitation: (placeId: number, itemId: number, guest: number, role: Role, ends?: string) =>
		call<{ access: Access[] }>("PUT", `${accessPath(placeId, itemId)}/guests/${guest}`, { role, ends: ends ?? null }).then(
			(r) => r.access,
		),
	/** Takes a guest's invitation away: when it was their last, they are signed out at once. */
	uninvite: (placeId: number, itemId: number, guest: number) =>
		call<{ access: Access[] }>("DELETE", `${accessPath(placeId, itemId)}/guests/${guest}`).then((r) => r.access),
	/** The public link on an item, or null. */
	link: (placeId: number, itemId: number) =>
		call<{ link: PublicLink | null }>("GET", `${base}/places/${placeId}/items/${itemId}/link`).then((r) => r.link),
	/**
	 * Makes a public link on an item; with replace, in place of the one it has, whose address then stops working. The
	 * answer holds the address, once.
	 */
	makeLink: (placeId: number, itemId: number, replace: boolean, kind: LinkKind = "view") =>
		call<{ link: PublicLink }>("POST", `${base}/places/${placeId}/items/${itemId}/link`, { replace, kind }).then((r) => r.link),
	/** Changes what a folder's link lets visitors do, at the same address. */
	setLinkKind: (placeId: number, itemId: number, kind: LinkKind) =>
		call<{ link: PublicLink }>("PUT", `${base}/places/${placeId}/items/${itemId}/link`, { kind }).then((r) => r.link),
	/** Lets an upload-only link take files again: what it has taken in counts from 0. */
	resetLink: (placeId: number, itemId: number) =>
		call<{ link: PublicLink }>("POST", `${base}/places/${placeId}/items/${itemId}/link/reset`).then((r) => r.link),
	/** Removes the public link on an item: its address stops working for good. */
	removeLink: (placeId: number, itemId: number) => call<null>("DELETE", `${base}/places/${placeId}/items/${itemId}/link`),
	usage: () => call<Usage>("GET", `${base}/usage`),
	/** Switches sharing outside the project off (or back on) for the whole project: a project admin. */
	switchOutside: (on: boolean) =>
		call<{ sharingOutside: OutsideSwitch }>("PUT", `${base}/sharing-outside`, { on }).then((r) => r.sharingOutside),
	/** Switches sharing outside the project off (or back on) for one shared folder: its managers. */
	switchFolderOutside: (placeId: number, on: boolean) => call<Place>("PUT", `${base}/places/${placeId}/sharing-outside`, { on }),
	/** Everything shared outside the project, for a project admin. */
	sharedOutside: () =>
		call<{ sharingOutside: OutsideSwitch; items: OutsideShare[] }>("GET", `${base}/oversight/shared-outside`),
	/** The space each person and each shared folder uses, for a project admin. */
	space: () => call<Space>("GET", `${base}/oversight/space`),
	/** Every held My files of someone who left, and the record of hand-overs and deletions, for a project admin. */
	held: () => call<{ held: Held[]; acts: HeldAct[] }>("GET", `${base}/held`),
	/** Hands a held My files, as a whole, to a member: done, or under way (the Drive goes on with it). */
	handOver: (placeId: number, to: string) =>
		call<{ act: HeldAct }>("POST", `${base}/held/${placeId}/hand-over`, { to }).then((r) => r.act),
	/** Deletes a held My files for good, its trash included. */
	deleteHeld: (placeId: number) => call<{ act: HeldAct }>("DELETE", `${base}/held/${placeId}`).then((r) => r.act),
	/**
	 * What is in a folder of a place (null: its top folder), from the Drive's index; opening it also has it looked at on
	 * the storage. With wait, the list once that look is done (the Drive waits up to ten seconds for it).
	 */
	list: (placeId: number, folderId: number | null, wait?: boolean) =>
		call<Listing>("GET", `${inFolder(placeId, folderId)}/items${wait ? "?wait" : ""}`),
	newFolder: (placeId: number, folderId: number | null, name: string) =>
		call<Item>("POST", `${inFolder(placeId, folderId)}/folders`, { name }),
	rename: (placeId: number, itemId: number, name: string) =>
		call<Item>("POST", `${base}/places/${placeId}/items/${itemId}/rename`, { name }),
	/**
	 * Moves an item into a folder (null: the top folder) of its own place, or of toPlace: out of the person's own My
	 * files into a shared folder, after the warning.
	 */
	move: (placeId: number, itemId: number, to: number | null, toPlace?: number) =>
		call<Item & { place: number }>("POST", `${base}/places/${placeId}/items/${itemId}/move`, toPlace === undefined ? { to } : { to, place: toPlace }),
	/** Who would see an item in a folder of toPlace (null: its top folder), before it moves there. */
	movePreview: (placeId: number, itemId: number, toPlace: number, to: number | null) => {
		const query = new URLSearchParams({ place: String(toPlace) });
		if (to !== null) query.set("to", String(to));
		return call<MovePreview>("GET", `${base}/places/${placeId}/items/${itemId}/move?${query}`);
	},
	/**
	 * Files and folders whose names match, across everything the person can see: the best 50 first, and `more` when a
	 * longer search would find others.
	 */
	search: (q: string) => call<{ items: Found[]; more: boolean }>("GET", `${base}/search?${new URLSearchParams({ q })}`),
	/** What others shared with the person that they reach only through it, the most recently shared first. */
	sharedWithMe: () => call<{ items: SharedItem[] }>("GET", `${base}/shared-with-me`).then((r) => r.items),
	/** Moves an item into the place's trash. */
	remove: (placeId: number, itemId: number) => call<TrashItem>("DELETE", `${base}/places/${placeId}/items/${itemId}`),
	trash: (placeId: number) => call<{ items: TrashItem[] }>("GET", `${base}/places/${placeId}/trash`).then((r) => r.items),
	restore: (placeId: number, itemId: number) =>
		call<{ item: Item; path: FolderRef[] }>("POST", `${base}/places/${placeId}/trash/${itemId}/restore`),
	upload,
	/**
	 * Starts a resumable upload of a large file into a folder of a place. As for an upload in one request, a name that
	 * is taken is refused with `exists` unless `existing` says what to do: before anything is sent.
	 */
	startUpload: (placeId: number, folderId: number | null, name: string, size: number, existing: Existing | undefined) => {
		const query = new URLSearchParams({ name, size: String(size) });
		if (existing) query.set("existing", existing);
		return call<Session>("POST", `${inFolder(placeId, folderId)}/uploads?${query}`);
	},
	/** Where a resumable upload stands: how much has arrived, and whether it waits its turn. */
	uploadState: (placeId: number, id: string) => call<Session>("GET", sessionPath(placeId, id)),
	/** Sends one part of a resumable upload, from offset, which must be where what has arrived ends. */
	uploadPart: (placeId: number, id: string, offset: number, part: Blob, onProgress: (sent: number) => void, signal?: AbortSignal) =>
		send<{ offset: number }>("PUT", `${sessionPath(placeId, id)}?offset=${offset}`, part, 200, onProgress, signal),
	/** Moves a whole resumable upload into its folder. A name taken meanwhile is refused with `exists`, as at the start. */
	finishUpload: (placeId: number, id: string, existing: Existing | undefined) =>
		call<Uploaded>("POST", `${sessionPath(placeId, id)}/finish${existing ? `?existing=${existing}` : ""}`),
	/** Cancels a resumable upload: what arrived of it is removed. */
	cancelUpload: (placeId: number, id: string) => call<null>("DELETE", sessionPath(placeId, id)),
	/** The address that downloads a file under its original name, whatever kind of file it is. */
	downloadUrl: (placeId: number, itemId: number) => `${base}/places/${placeId}/items/${itemId}/download`,
	/**
	 * The address that shows a file in the browser, for a kind that opens there (`opens`); any other file downloads
	 * from it.
	 */
	openUrl: (placeId: number, itemId: number) => `${base}/places/${placeId}/items/${itemId}/open`,
	/**
	 * The address that downloads a folder of a place (null: its top folder) as one zip; with items, only those items
	 * of it.
	 */
	zipUrl: (placeId: number, folderId: number | null, items?: number[]) => {
		const query = new URLSearchParams();
		for (const id of items ?? []) query.append("item", String(id));
		return `${inFolder(placeId, folderId)}/zip${items ? `?${query}` : ""}`;
	},
};

/** What a guest may do with a file or folder given to them (internal/access): each decided again on every request. */
export type GuestAction =
	| "list-as-guest"
	| "download-as-guest"
	| "upload-as-guest"
	| "make-folder-as-guest"
	| "rename-as-guest"
	| "move-as-guest"
	| "delete-as-guest";

/** A file or folder as a guest sees it: what they may do with it, and nothing about who else can see it. */
export type GuestItem = Item & { can: GuestAction[] };

/** What is in a folder given to a guest, and the folders down to it that they may open, from the one given to them. */
export type GuestListing = {
	items: GuestItem[];
	path: FolderRef[];
	role: "viewer" | "editor" | "";
	can: GuestAction[];
	checking?: boolean;
};

/**
 * An item of a guest's "Shared with me": what was shared with them that lets them in now. `sharedBy` is the name of who
 * shared it (or the project's); `until` when their access ends, when it does.
 */
export type GuestShared = GuestItem & { place: number; role: "viewer" | "editor"; sharedBy: string; shared: string; until?: string };

/** The guests' own calls, under the public prefix (after forGuests). Their uploads and downloads are api's. */
export const guestApi = {
	/** Who is signed in; an ApiError with `signIn` when nobody is. */
	me: () => call<{ address: string; project: string }>("GET", `${base}/me`),
	/** Asks for a code mailed to the address: the answer is the same whatever the address. */
	askForCode: (address: string) => call<{ message: string }>("POST", `${base}/code`, { address }),
	/** Signs in with the code: the Drive keeps the guest signed in on this browser for 30 days. */
	signIn: (address: string, code: string) => call<{ address: string }>("POST", `${base}/sign-in`, { address, code }),
	signOut: () => call<null>("POST", `${base}/sign-out`),
	/**
	 * What was shared with the guest that lets them in now; `unavailable` says something else given to them is not
	 * available while sharing outside the project is switched off (it is not named).
	 */
	sharedWithMe: () => call<{ items: GuestShared[]; unavailable?: boolean }>("GET", `${base}/shared-with-me`),
	/** One file or folder, with the folders above it that the guest may open. */
	item: (placeId: number, itemId: number) =>
		call<{ item: GuestItem; path: FolderRef[]; role: string }>("GET", `${base}/places/${placeId}/items/${itemId}`),
	list: (placeId: number, folderId: number, wait?: boolean) =>
		call<GuestListing>("GET", `${base}/places/${placeId}/items/${folderId}/items${wait ? "?wait" : ""}`),
	/** Moves an item into a folder inside what was given to the guest. */
	move: (placeId: number, itemId: number, to: number) => call<Item>("POST", `${base}/places/${placeId}/items/${itemId}/move`, { to }),
	/** Deletes an item into its owner's trash. */
	remove: (placeId: number, itemId: number) => call<null>("DELETE", `${base}/places/${placeId}/items/${itemId}`),
};
