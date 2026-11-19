import { useQuery, useQueryClient } from "@tanstack/react-query";
import { User, Users, X } from "lucide-react";
import { useId, useState } from "react";
import { nameOf, ROLE_MEANS, ROLE_NAME, whyOf } from "../access";
import { type Access, api, type Grantee, type Role } from "../api";
import { Button } from "./button";

type Props = {
	placeId: number;
	/** The item, or null for a shared folder as a whole. */
	itemId: number | null;
	/** What it is, for the words: a file, a folder, or a shared folder as a whole. */
	what: "file" | "folder" | "shared folder";
	/** True when the person may change who has access here: share an item, or manage a shared folder. */
	canChange: boolean;
	/** The roles that can be given here: viewer and editor, and manager on a shared folder as a whole. */
	roles: Role[];
	/** The person asking, so their own line says "You". */
	me?: string;
	/** Called after a change, so the list and the places follow it. */
	onChanged: () => void;
};

const EVERYONE = "everyone";

const granteeOf = (a: Access): Grantee => (a.everyone ? { everyone: true } : { userId: a.userId ?? "" });

/**
 * "Who can see this" and "Give access", in the details panel: everyone with access to an item (or to a shared folder
 * as a whole), with where it comes from, and, for whoever may share it, giving access to a member or to everyone in
 * the project, changing a role and taking it away. Access from a folder above is changed on that folder.
 */
export function AccessSection({ placeId, itemId, what, canChange, roles, me, onChanged }: Props) {
	const queryClient = useQueryClient();
	const key = ["access", placeId, itemId];
	const access = useQuery({ queryKey: key, queryFn: () => api.access(placeId, itemId) });
	const members = useQuery({ queryKey: ["members"], queryFn: api.members, enabled: canChange });
	const [who, setWho] = useState("");
	const [role, setRole] = useState<Role>("viewer");
	const [problem, setProblem] = useState<string>();
	const [busy, setBusy] = useState(false);
	const pickerId = useId();
	const rolesId = useId();

	const change = async (run: () => Promise<Access[]>) => {
		setBusy(true);
		setProblem(undefined);
		try {
			queryClient.setQueryData(key, await run());
			setWho("");
			onChanged();
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
			void access.refetch();
		} finally {
			setBusy(false);
		}
	};

	const owner = access.data?.find((a) => a.owner)?.userId;
	// Not the owner, who has it already, and not the person asking: they change their own line instead.
	const choices = members.data?.filter((m) => m.userId !== owner && m.userId !== me) ?? [];
	const offered = who === EVERYONE ? roles.filter((r) => r !== "manager") : roles;
	const chosenRole = offered.includes(role) ? role : "viewer";

	return (
		<>
			<h3 className="mt-6 text-sm font-semibold">Who can see this</h3>
			{what !== "file" && (
				<p className="mt-0.5 text-sm text-muted-foreground">
					{what === "shared folder"
						? "Its managers decide who has access to it. Project admins are managers of every shared folder. Access on it reaches everything in it."
						: "Access on a folder reaches everything in it."}
				</p>
			)}
			{access.error ? (
				<p className="mt-2 text-sm text-destructive">{access.error.message}</p>
			) : !access.data ? (
				<p className="mt-2 text-sm text-muted-foreground">Loading…</p>
			) : (
				<ul className="mt-2 text-sm">
					{access.data.map((a, i) => {
						const name = a.everyone
							? "Everyone in the project"
							: a.former
								? "Someone no longer in the project"
								: a.userId === me
									? "You"
									: nameOf(a);
						const why = whyOf(a);
						const changeable = canChange && a.given && !a.owner && !a.admin && a.role !== "owner";
						const Icon = a.everyone ? Users : User;
						return (
							// One person can have several lines, from several folders: the place in the list is the key.
							<li key={i} className="flex items-center gap-2.5 border-b border-border py-2 last:border-0">
								<Icon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
								<div className="min-w-0 flex-1">
									<p className="truncate" title={a.email}>
										{name}
									</p>
									{!changeable && (
										<p className="truncate text-muted-foreground">
											{ROLE_NAME[a.role]}
											{why ? ` · ${why}` : ""}
										</p>
									)}
								</div>
								{changeable && (
									<>
										<select
											aria-label={`Role of ${name}`}
											value={a.role}
											disabled={busy}
											onChange={(e) =>
												void change(() => api.giveAccess(placeId, itemId, granteeOf(a), e.target.value as Role))
											}
											className="h-8 rounded-md border border-input bg-card px-1.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
										>
											{(a.everyone ? roles.filter((r) => r !== "manager") : roles).map((r) => (
												<option key={r} value={r}>
													{ROLE_NAME[r]}
												</option>
											))}
										</select>
										<Button
											small
											variant="ghost"
											aria-label={`Take away ${name === "You" ? "your" : `${name}’s`} access`}
											title="Take away"
											disabled={busy}
											onClick={() => void change(() => api.takeAccess(placeId, itemId, granteeOf(a)))}
										>
											<X aria-hidden />
										</Button>
									</>
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
						if (!who) return;
						const to: Grantee = who === EVERYONE ? { everyone: true } : { userId: who };
						void change(() => api.giveAccess(placeId, itemId, to, chosenRole));
					}}
				>
					<label htmlFor={pickerId} className="text-sm font-semibold">
						Give access
					</label>
					<select
						id={pickerId}
						value={who}
						onChange={(e) => setWho(e.target.value)}
						className="mt-2 h-9 w-full min-w-0 rounded-md border border-input bg-card px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
					>
						<option value="">{members.isPending ? "Loading…" : "Choose who"}</option>
						<option value={EVERYONE}>Everyone in the project</option>
						{choices.map((m) => (
							<option key={m.userId} value={m.userId}>
								{m.displayName ? `${m.displayName} (${m.email})` : m.email}
							</option>
						))}
					</select>
					<fieldset className="mt-3" aria-describedby={rolesId}>
						<legend className="sr-only">Role</legend>
						{offered.map((r) => (
							<label key={r} className="flex cursor-pointer gap-2.5 rounded-md py-1.5 text-sm">
								<input
									type="radio"
									name={`${pickerId}-role`}
									value={r}
									checked={chosenRole === r}
									onChange={() => setRole(r)}
									className="mt-0.5 size-4 shrink-0 accent-foreground"
								/>
								<span>
									<span className="font-medium">{ROLE_NAME[r]}</span>
									<span className="block text-muted-foreground">{ROLE_MEANS[r]}</span>
								</span>
							</label>
						))}
					</fieldset>
					<p id={rolesId} className="sr-only">
						Choose what they may do.
					</p>
					<Button type="submit" className="mt-3" disabled={busy || !who}>
						Give access
					</Button>
				</form>
			) : (
				<p className="mt-4 text-sm text-muted-foreground">
					{what === "shared folder"
						? "Only its managers can change who has access to it."
						: "You can open it but not share it: only its editors can share it further."}
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
