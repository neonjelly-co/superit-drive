// How the interface says who can see something, and what each role means. English for now; Hungarian comes with its
// own ticket.
import type { Access, Member, RoleAt, SeenBy } from "./api";

export const ROLE_NAME: Record<RoleAt, string> = { viewer: "Viewer", editor: "Editor", manager: "Manager", owner: "Owner" };

/**
 * What each role means, said where it is chosen. "Editor" is about the files and folders, never about changing what
 * is inside a file.
 */
export const ROLE_MEANS = {
	viewer: "Can open and download.",
	editor: "Can also upload, rename, move and delete, and share it further. Does not mean changing what is inside a file.",
	manager: "Can also decide who has access to the shared folder, and delete it.",
} as const;

/** A member's name as people read it: their display name, or their email address. */
export const nameOf = (p: { displayName?: string; email?: string }) => p.displayName || p.email || "";

/** The short form of a member's name, for the list's sentence: the first word of their name, or of their address. */
function shortName(m: Member | undefined): string {
	if (!m) return "someone";
	if (m.displayName) return m.displayName.split(/\s+/)[0] ?? m.displayName;
	return m.email.split("@")[0] ?? m.email;
}

/**
 * The list's one plain sentence: who can see an item. "Only you", "You and Dóra", "You, Dóra and Bence", "You and 4
 * others", "Everyone in the project", or, when a public link reaches it, "Anyone with the link"; and when guests can,
 * that too: "You and a guest", "Everyone in the project and 2 guests".
 */
export function whoCanSee(seenBy: SeenBy, me: string | undefined, members: Member[] | undefined): string {
	if (seenBy.link) return "Anyone with the link";
	const guests = seenBy.guests ?? 0;
	if (guests === 0) return whoCanSeeInside(seenBy, me, members);
	const them = guests === 1 ? "a guest" : `${guests} guests`;
	const inside = whoCanSeeInside(seenBy, me, members);
	return inside ? `${inside} and ${them}` : them.charAt(0).toUpperCase() + them.slice(1);
}

function whoCanSeeInside(seenBy: SeenBy, me: string | undefined, members: Member[] | undefined): string {
	if (seenBy.everyone) return "Everyone in the project";
	const byId = new Map(members?.map((m) => [m.userId, m]));
	const others = seenBy.members.filter((id) => id !== me).map((id) => shortName(byId.get(id)));
	const withMe = me !== undefined && seenBy.members.includes(me);
	if (!withMe) {
		if (others.length <= 2) return others.join(" and ");
		return `${others.length} people`;
	}
	if (others.length === 0) return "Only you";
	if (others.length === 1) return `You and ${others[0]}`;
	if (others.length === 2) return `You, ${others[0]} and ${others[1]}`;
	return `You and ${others.length} others`;
}

/** Who a line of "who can see this" is about, as the person reading it says it. */
export function whoOf(a: Access, me: string | undefined): string {
	if (a.link) return a.linkKind === "upload-only" ? "Anyone with the upload-only link" : "Anyone with the link";
	if (a.guest) return a.guest.address;
	if (a.everyone) return "Everyone in the project";
	if (a.former) return "Someone no longer in the project";
	return a.userId === me ? "You" : nameOf(a);
}

/**
 * What a line of "who can see this" lets them do, in a word or a few: their role, or, through an upload-only link,
 * that they send files in.
 */
export function roleOf(a: Access): string {
	if (!a.role) return a.link ? "Can only send files in" : "";
	return ROLE_NAME[a.role];
}

/** What a public link lets its visitors do, said where it is chosen. */
export const LINK_KIND_MEANS = {
	view: { name: "Open and download", means: "They see what is in it, and download it." },
	"upload-only": {
		name: "Only send files in",
		means: "They send files into this folder. They cannot see what is in it, not even the names, and replace nothing.",
	},
} as const;

/** Why someone in "who can see this" has their access, in a few words after their role. */
export function whyOf(a: Access): string {
	if (a.admin) return "project admin";
	if (a.inherited) return a.from ? `from “${a.from.name}”` : "from a folder above";
	return "";
}

const day = new Intl.DateTimeFormat("en-GB", { dateStyle: "medium" });
const moment = new Intl.DateTimeFormat("en-GB", { dateStyle: "medium", timeStyle: "short" });

/**
 * What else a line says: a guest's ("Guest"), until when, whether it lets them in now, and how often they used it. Each
 * part is a few words.
 */
export function moreOf(a: Access): string[] {
	const out: string[] = [];
	if (a.guest) out.push("Guest");
	if (a.until) out.push(new Date(a.until) > new Date() ? `until ${day.format(new Date(a.until))}` : `ended ${day.format(new Date(a.until))}`);
	if (a.guest && a.works === false && !(a.until && new Date(a.until) <= new Date())) out.push("does not let them in now");
	if (a.uses !== undefined) {
		out.push(a.uses === 0 ? "not used yet" : `used ${a.uses === 1 ? "once" : `${a.uses} times`}${a.used ? `, last ${moment.format(new Date(a.used))}` : ""}`);
	}
	return out;
}

/** What an invitation lets a guest do, said where it is given. */
export const GUEST_ROLE_MEANS = {
	viewer: "Can open and download.",
	editor: "Can also upload, rename, move and delete inside it. Cannot share it.",
} as const;
