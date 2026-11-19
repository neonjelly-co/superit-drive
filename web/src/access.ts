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
 * others", or "Everyone in the project".
 */
export function whoCanSee(seenBy: SeenBy, me: string | undefined, members: Member[] | undefined): string {
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

/** Why someone in "who can see this" has their access, in a few words after their role. */
export function whyOf(a: Access): string {
	if (a.admin) return "project admin";
	if (a.inherited) return a.from ? `from “${a.from.name}”` : "from a folder above";
	return "";
}
