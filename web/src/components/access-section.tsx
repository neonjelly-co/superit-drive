import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link2, Mail, User, Users, X } from "lucide-react";
import { useId, useState } from "react";
import { guestRoleMeans, isMe, moreOf, nameOf, roleMeans, roleName, roleOf, whoOf, whyOf } from "../access";
import { type Access, api, type Grantee, type Member, type Role } from "../api";
import { t } from "../i18n";
import { Button } from "./button";

type Props = {
	placeId: number;
	/** The item, or null for a shared folder as a whole. */
	itemId: number | null;
	/** What it is, for the words: a file, a folder, or a shared folder as a whole. */
	what: "file" | "folder" | "shared folder";
	/** True when the person may change who has access here: share an item, or manage a shared folder. */
	canChange: boolean;
	/** True when the person may invite a guest here: whoever may share an item, while sharing outside the project is on. */
	canInvite?: boolean;
	/** The roles that can be given here: viewer and editor, and manager on a shared folder as a whole. */
	roles: Role[];
	/** The person asking, so their own line says "You". */
	me?: string;
	/** Called after a change, so the list and the places follow it. */
	onChanged: () => void;
};

const granteeOf = (a: Access): Grantee => (a.everyone ? { everyone: true } : { userId: a.userId ?? "" });

/** Whether what was typed is meant as an email address. */
const looksLikeAddress = (typed: string) => /^[^\s@]+@[^\s@]+$/.test(typed.trim());

/** The member a typed name or address is, if it is one. */
function memberOf(typed: string, members: Member[] | undefined): Member | undefined {
	const t = typed.trim().toLowerCase();
	if (!t) return undefined;
	return members?.find((m) => m.email.toLowerCase() === t || (m.displayName !== "" && m.displayName.toLowerCase() === t));
}

/** An end date picked as a day (yyyy-mm-dd): the access ends when that day ends, in the browser's time zone. */
function endOfDay(day: string): string | undefined {
	if (!day) return undefined;
	const [y, m, d] = day.split("-").map(Number);
	if (!y || !m || !d) return undefined;
	return new Date(y, m - 1, d + 1).toISOString();
}

/** The day (yyyy-mm-dd) an end date picked with endOfDay was picked as. */
function dayOf(iso: string | undefined): string {
	if (!iso) return "";
	const t = new Date(new Date(iso).getTime() - 1);
	return `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, "0")}-${String(t.getDate()).padStart(2, "0")}`;
}

/** Today, as a day an end date can be picked as. */
const today = () => dayOf(new Date(Date.now() + 1).toISOString());

const field =
	"h-9 w-full min-w-0 rounded-md border border-input bg-card px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 max-md:h-11 max-md:text-base";

/**
 * "Who can see this" and "Give access", in the details panel: everyone with access to an item (or to a shared folder
 * as a whole), with where it comes from, and, for whoever may share it, giving access by typing a member's name or
 * address, everyone in the project, or anyone else's address (a guest, invited by mail, until a day if wanted),
 * changing a role or an end date, and taking it away. Access from a folder above is changed on that folder.
 */
export function AccessSection({ placeId, itemId, what, canChange, canInvite, roles, me, onChanged }: Props) {
	const queryClient = useQueryClient();
	const key = ["access", placeId, itemId];
	const access = useQuery({ queryKey: key, queryFn: () => api.access(placeId, itemId) });
	const members = useQuery({ queryKey: ["members"], queryFn: api.members, enabled: canChange });
	const meQuery = useQuery({ queryKey: ["me"], queryFn: api.me, enabled: canChange });
	const [who, setWho] = useState("");
	const [role, setRole] = useState<Role>("viewer");
	const [until, setUntil] = useState("");
	const [problem, setProblem] = useState<string>();
	const [notice, setNotice] = useState<string>();
	const [busy, setBusy] = useState(false);
	const pickerId = useId();
	const listId = useId();
	const rolesId = useId();
	const untilId = useId();

	/** Makes a change, and shows who has access after it; said, made from the answer, tells the person what it did. */
	const change = async <T extends { access: Access[] }>(run: () => Promise<T>, said?: (r: T) => string | undefined) => {
		setBusy(true);
		setProblem(undefined);
		setNotice(undefined);
		try {
			const r = await run();
			queryClient.setQueryData(key, r.access);
			setNotice(said?.(r));
			setWho("");
			setUntil("");
			onChanged();
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
			void access.refetch();
		} finally {
			setBusy(false);
		}
	};
	const lines = (p: Promise<Access[]>) => p.then((access) => ({ access }));

	const owner = access.data?.find((a) => a.owner)?.userId;
	// Not the owner, who has it already, and not the person asking: they change their own line instead.
	const choices = members.data?.filter((m) => m.userId !== owner && m.userId !== me) ?? [];
	const everyone = who.trim().toLowerCase() === t.who.everyone.toLowerCase();
	const member = everyone ? undefined : memberOf(who, members.data);
	// An address that is no member's: a guest, on a file or folder only.
	const guest = !everyone && !member && looksLikeAddress(who);
	const offered = everyone || guest ? roles.filter((r) => r !== "manager") : roles;
	const chosenRole = offered.includes(role) ? role : "viewer";
	const mail = meQuery.data?.guests;
	let guestWhy: string | undefined;
	if (guest && itemId === null) guestWhy = t.access.guestsNotOnSharedFolder;
	else if (guest && !canInvite) guestWhy = t.access.guestsSwitchedOff;
	else if (guest && mail && !mail.invite) guestWhy = mail.why;

	const give = () => {
		if (everyone) return change(() => lines(api.giveAccess(placeId, itemId, { everyone: true }, chosenRole)));
		if (member && (itemId === null || !looksLikeAddress(who))) {
			return change(() => lines(api.giveAccess(placeId, itemId, { userId: member.userId }, chosenRole)));
		}
		if (itemId === null || (!member && !guest)) {
			setProblem(t.access.chooseSomeone);
			return;
		}
		// By address: the Drive decides whether it is a member's or a guest's, and says which.
		return change(
			() => api.invite(placeId, itemId, who.trim(), chosenRole, guest ? endOfDay(until) : undefined),
			(r) => {
				if (r.member) return (until ? t.access.sharedAsMemberNoEnd : t.access.sharedAsMember)(nameOf(r.member));
				if (r.guest && r.invited) return t.access.invitedGuest(r.guest.address);
				return r.guest ? t.access.invitedAlready(r.guest.address) : undefined;
			},
		);
	};

	return (
		<>
			<h3 className="mt-6 text-sm font-semibold">{t.access.whoCanSeeThis}</h3>
			{what !== "file" && (
				<p className="mt-0.5 text-sm text-muted-foreground">
					{what === "shared folder" ? t.access.sharedFolderNote : t.access.folderNote}
				</p>
			)}
			{access.error ? (
				<p className="mt-2 text-sm text-destructive">{access.error.message}</p>
			) : !access.data ? (
				<p className="mt-2 text-sm text-muted-foreground">{t.common.loading}</p>
			) : (
				<ul className="mt-2 text-sm">
					{access.data.map((a, i) => {
						const name = whoOf(a, me);
						const why = whyOf(a);
						// A public link is made and removed in its own section, below.
						const changeable = canChange && a.given && !a.owner && !a.admin && !a.link && a.role !== "owner";
						// Nobody gives access on an item to themselves: their own line can only be taken away (stepping back).
						const ownOnItem = itemId !== null && !a.everyone && !a.guest && a.userId === me;
						const guestId = a.guest?.id;
						// A guest's role and end date change only where a guest could be invited now.
						const roleChangeable = changeable && !ownOnItem && (guestId === undefined || (canInvite && itemId !== null));
						const Icon = a.link ? Link2 : a.everyone ? Users : a.guest ? Mail : User;
						const said = [...(roleChangeable ? [] : [roleOf(a)]), ...(why ? [why] : []), ...moreOf(a)].filter(Boolean);
						return (
							// One person can have several lines, from several folders: the place in the list is the key.
							// A guest's line holds their end date as well: on a phone its role and its X go on a line of their own.
							<li key={i} className="flex flex-wrap items-center gap-x-2.5 border-b border-border py-2 last:border-0">
								<Icon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
								<div className={`min-w-0 flex-1 ${roleChangeable && guestId !== undefined ? "max-md:basis-[calc(100%-1.625rem)]" : ""}`}>
									<p className={`truncate ${a.guest ? "font-mono" : ""}`} title={a.email ?? a.guest?.address}>
										{name}
									</p>
									{said.length > 0 && <p className="truncate text-muted-foreground max-md:whitespace-normal">{said.join(" · ")}</p>}
									{roleChangeable && guestId !== undefined && itemId !== null && (
										<label className="mt-1 flex items-center gap-2 text-muted-foreground">
											{t.access.until}
											<input
												type="date"
												min={today()}
												value={dayOf(a.until)}
												disabled={busy}
												onChange={(e) =>
													void change(() =>
														lines(api.changeInvitation(placeId, itemId, guestId, a.role as Role, endOfDay(e.target.value))),
													)
												}
												className="h-8 rounded-md border border-input bg-card px-1.5 text-sm max-md:h-11 max-md:text-base"
												aria-label={t.access.endDateOf(name)}
											/>
										</label>
									)}
								</div>
								{roleChangeable && (
									<select
										aria-label={t.access.roleOf(name)}
										value={a.role}
										disabled={busy}
										onChange={(e) =>
											void change(() =>
												lines(
													guestId !== undefined && itemId !== null
														? api.changeInvitation(placeId, itemId, guestId, e.target.value as Role, a.until)
														: api.giveAccess(placeId, itemId, granteeOf(a), e.target.value as Role),
												),
											)
										}
										className={`h-8 rounded-md border border-input bg-card px-1.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 max-md:h-11 max-md:text-base ${guestId !== undefined ? "max-md:mt-1 max-md:ml-[1.625rem]" : ""}`}
									>
										{(a.everyone || a.guest ? roles.filter((r) => r !== "manager") : roles).map((r) => (
											<option key={r} value={r}>
												{roleName(r)}
											</option>
										))}
									</select>
								)}
								{changeable && (
									<Button
										small
										variant="ghost"
										aria-label={isMe(a, me) ? t.access.takeAwayYours : t.access.takeAwayTheirs(name)}
										title={t.access.takeAway}
										disabled={busy}
										onClick={() =>
											void change(() =>
												lines(
													guestId !== undefined && itemId !== null
														? api.uninvite(placeId, itemId, guestId)
														: api.takeAccess(placeId, itemId, granteeOf(a)),
												),
											)
										}
									>
										<X aria-hidden />
									</Button>
								)}
							</li>
						);
					})}
				</ul>
			)}

			{canChange ? (
				<form
					className="mt-6"
					onSubmit={(e) => {
						e.preventDefault();
						if (who.trim()) void give();
					}}
				>
					<label htmlFor={pickerId} className="text-sm font-semibold">
						{t.access.giveAccess}
					</label>
					<input
						id={pickerId}
						list={listId}
						value={who}
						onChange={(e) => {
							setWho(e.target.value);
							setProblem(undefined);
							setNotice(undefined);
						}}
						placeholder={itemId === null ? t.access.placeholderMember : t.access.placeholder}
						autoComplete="off"
						spellCheck={false}
						className={`mt-2 ${field}`}
					/>
					<datalist id={listId}>
						<option value={t.who.everyone} />
						{choices.map((m) => (
							<option key={m.userId} value={m.email}>
								{m.displayName || m.email}
							</option>
						))}
					</datalist>
					{member && <p className="mt-1.5 text-sm text-muted-foreground">{t.access.aMember(nameOf(member))}</p>}
					{guest && !guestWhy && <p className="mt-1.5 text-sm text-muted-foreground">{t.access.guestNote}</p>}
					{guestWhy && (
						<p role="status" className="mt-1.5 text-sm text-attention">
							{guestWhy}
						</p>
					)}
					<fieldset className="mt-3" aria-describedby={rolesId}>
						<legend className="sr-only">{t.access.role}</legend>
						{offered.map((r) => (
							<label key={r} className="flex cursor-pointer gap-2.5 rounded-md py-1.5 text-sm max-md:py-3">
								<input
									type="radio"
									name={`${pickerId}-role`}
									value={r}
									checked={chosenRole === r}
									onChange={() => setRole(r)}
									className="mt-0.5 size-4 shrink-0 accent-foreground"
								/>
								<span>
									<span className="font-medium">{roleName(r)}</span>
									<span className="block text-muted-foreground">{guest && r !== "manager" ? guestRoleMeans(r) : roleMeans(r)}</span>
								</span>
							</label>
						))}
					</fieldset>
					<p id={rolesId} className="sr-only">
						{t.access.chooseRole}
					</p>
					{guest && !guestWhy && (
						<div className="mt-2 text-sm">
							<label htmlFor={untilId} className="font-medium">
								{t.access.until} <span className="font-normal text-muted-foreground">{t.access.ifYouLike}</span>
							</label>
							<input
								id={untilId}
								type="date"
								min={today()}
								value={until}
								onChange={(e) => setUntil(e.target.value)}
								className={`mt-1 ${field}`}
							/>
							<p className="mt-1 text-muted-foreground">{t.access.untilNote}</p>
						</div>
					)}
					<Button type="submit" className="mt-3" disabled={busy || !who.trim() || guestWhy !== undefined}>
						{guest ? t.access.invite : t.access.giveAccess}
					</Button>
				</form>
			) : (
				<p className="mt-4 text-sm text-muted-foreground">
					{what === "shared folder" ? t.access.onlyManagersChange : t.access.cannotShare}
				</p>
			)}
			{notice && (
				<p role="status" className="mt-3 text-sm text-muted-foreground">
					{notice}
				</p>
			)}
			{problem && (
				<p role="alert" className="mt-3 text-sm text-destructive">
					{problem}
				</p>
			)}
		</>
	);
}
