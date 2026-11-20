// How the interface says who can see something, and what each role means, in the page's language.
import type { Access, Member, RoleAt, SeenBy } from "./api";
import { formatDay, formatMoment } from "./format";
import { t } from "./i18n";

/** A role's name: "Viewer", "Editor", "Manager", "Owner". */
export const roleName = (r: RoleAt): string => t.common[r];

/**
 * What each role means, said where it is chosen. "Editor" is about the files and folders, never about changing what
 * is inside a file.
 */
export const roleMeans = (r: "viewer" | "editor" | "manager"): string => t.roles[r];

/** What an invitation lets a guest do, said where it is given. */
export const guestRoleMeans = (r: "viewer" | "editor"): string => (r === "editor" ? t.roles.guestEditor : t.roles.guestViewer);

/** A member's name as people read it: their display name, or their email address. */
export const nameOf = (p: { displayName?: string; email?: string }) => p.displayName || p.email || "";

/** The short form of a member's name, for the list's sentence: the first word of their name, or of their address. */
function shortName(m: Member | undefined): string {
	if (!m) return t.who.someone;
	if (m.displayName) return m.displayName.split(/\s+/)[0] ?? m.displayName;
	return m.email.split("@")[0] ?? m.email;
}

/**
 * The list's one plain sentence: who can see an item. "Only you", "You and Dóra", "You, Dóra and Bence", "You and 4
 * others", "Everyone in the project", or, when a public link reaches it, "Anyone with the link"; and when guests can,
 * that too: "You and a guest", "Everyone in the project and 2 guests".
 */
export function whoCanSee(seenBy: SeenBy, me: string | undefined, members: Member[] | undefined): string {
	if (seenBy.link) return t.who.anyoneWithLink;
	const guests = seenBy.guests ?? 0;
	if (guests === 0) return whoCanSeeInside(seenBy, me, members);
	const inside = whoCanSeeInside(seenBy, me, members);
	return inside ? t.who.andGuests(inside, t.who.guests(guests)) : t.who.onlyGuests(guests);
}

function whoCanSeeInside(seenBy: SeenBy, me: string | undefined, members: Member[] | undefined): string {
	if (seenBy.everyone) return t.who.everyone;
	const byId = new Map(members?.map((m) => [m.userId, m]));
	const others = seenBy.members.filter((id) => id !== me).map((id) => shortName(byId.get(id)));
	const withMe = me !== undefined && seenBy.members.includes(me);
	if (!withMe) {
		if (others.length === 0) return "";
		if (others.length === 1) return t.who.one(others[0] ?? "");
		if (others.length === 2) return t.who.two(others[0] ?? "", others[1] ?? "");
		return t.who.people(others.length);
	}
	if (others.length === 0) return t.who.onlyYou;
	if (others.length === 1) return t.who.youAnd(others[0] ?? "");
	if (others.length === 2) return t.who.youAndTwo(others[0] ?? "", others[1] ?? "");
	return t.who.youAndMore(others.length);
}

/** Whether a line of "who can see this" is about the person reading it. */
export const isMe = (a: Access, me: string | undefined) =>
	!a.link && !a.guest && !a.everyone && !a.former && me !== undefined && a.userId === me;

/** Who a line of "who can see this" is about, as the person reading it says it. */
export function whoOf(a: Access, me: string | undefined): string {
	if (a.link) return a.linkKind === "upload-only" ? t.who.anyoneWithUploadLink : t.who.anyoneWithLink;
	if (a.guest) return a.guest.address;
	if (a.everyone) return t.who.everyone;
	if (a.former) return t.common.someoneWhoLeft;
	return isMe(a, me) ? t.who.you : nameOf(a);
}

/**
 * What a line of "who can see this" lets them do, in a word or a few: their role, or, through an upload-only link,
 * that they send files in.
 */
export function roleOf(a: Access): string {
	if (!a.role) return a.link ? t.who.canOnlySendIn : "";
	return roleName(a.role);
}

/** Why someone in "who can see this" has their access, in a few words after their role. */
export function whyOf(a: Access): string {
	if (a.admin) return t.who.projectAdmin;
	if (a.inherited) return a.from ? t.who.from(a.from.name) : t.who.fromAbove;
	return "";
}

/**
 * What else a line says: a guest's ("Guest"), until when, whether it lets them in now, and how often they used it. Each
 * part is a few words.
 */
export function moreOf(a: Access): string[] {
	const out: string[] = [];
	if (a.guest) out.push(t.who.guest);
	if (a.until) out.push(new Date(a.until) > new Date() ? t.who.until(formatDay(a.until)) : t.who.ended(formatDay(a.until)));
	if (a.guest && a.works === false && !(a.until && new Date(a.until) <= new Date())) out.push(t.who.notLettingIn);
	if (a.uses !== undefined) {
		if (a.uses === 0) out.push(t.who.notUsedYet);
		else {
			const used = a.uses === 1 ? t.who.usedOnce : t.who.usedTimes(a.uses);
			out.push(a.used ? t.who.lastUsed(used, formatMoment(a.used)) : used);
		}
	}
	return out;
}
