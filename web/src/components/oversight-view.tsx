import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link2, UserRound } from "lucide-react";
import { useState } from "react";
import { nameOf } from "../access";
import { api, type Held, type HeldAct, type OutsideShare, type OutsideSwitch, type SpaceUsed } from "../api";
import { formatChanged, formatChangedFull, formatSize } from "../format";
import { Button } from "./button";
import { Dialog } from "./dialog";
import { FileIcon } from "./file-icon";

/**
 * "Sharing and space", for a project admin: the switch of sharing outside the project for the whole project, everything
 * shared outside it (public links and guests: the item's name, where it is, who shared it, with whom, when last used and
 * how often, and whether it works now), the My files of people who left (to hand over or delete), and how much space
 * each person and each shared folder uses. Names of items shared outside the project, and sums of sizes: never what is
 * in anything.
 */
export function OversightView() {
	const outside = useQuery({ queryKey: ["shared-outside"], queryFn: api.sharedOutside });
	const space = useQuery({ queryKey: ["space"], queryFn: api.space });
	// While a hand-over or deletion is under way, the list is asked again now and then until it is done.
	const held = useQuery({
		queryKey: ["held"],
		queryFn: api.held,
		refetchInterval: (q) => (q.state.data?.held.some((h) => h.act) ? 5000 : false),
	});
	return (
		<>
			<h1 className="text-xl font-semibold">Sharing and space</h1>
			<p className="mt-1 text-sm text-muted-foreground">
				What leaves the project and how much space each person and shared folder uses. You see names and sizes, never what
				is inside, and My files stay closed to you.
			</p>
			<ProjectSwitch current={outside.data?.sharingOutside} />
			<section aria-labelledby="shared-outside" className="mt-8">
				<h2 id="shared-outside" className="text-lg font-semibold">
					Shared outside the project
				</h2>
				<p className="mt-1 text-sm text-muted-foreground">
					Every public link and every guest, on what is in the Drive now. Whoever shared it can remove it in its details.
				</p>
				{outside.error ? (
					<Problem error={outside.error} onRetry={() => void outside.refetch()} />
				) : !outside.data ? (
					<p className="mt-4 text-sm text-muted-foreground">Loading…</p>
				) : outside.data.items.length === 0 ? (
					<p className="mt-4 text-sm text-muted-foreground">Nothing is shared outside the project.</p>
				) : (
					<SharedOutsideList items={outside.data.items} />
				)}
			</section>
			<section aria-labelledby="held" className="mt-8">
				<h2 id="held" className="text-lg font-semibold">
					People who left
				</h2>
				<p className="mt-1 text-sm text-muted-foreground">
					When someone leaves the project, their My files is kept, closed to everyone, you included, and nothing they shared
					from it works. If they come back, it is theirs again. You can hand it to another member or delete it; either is
					recorded with your name.
				</p>
				{held.error ? (
					<Problem error={held.error} onRetry={() => void held.refetch()} />
				) : !held.data ? (
					<p className="mt-4 text-sm text-muted-foreground">Loading…</p>
				) : (
					<>
						{held.data.held.length === 0 ? (
							<p className="mt-4 text-sm text-muted-foreground">Nobody who left has files in the Drive.</p>
						) : (
							<HeldList rows={held.data.held} />
						)}
						{held.data.acts.some((a) => a.done) && <ActRecord acts={held.data.acts.filter((a) => a.done)} />}
					</>
				)}
			</section>
			<section aria-labelledby="space" className="mt-8">
				<h2 id="space" className="text-lg font-semibold">
					Space used
				</h2>
				<p className="mt-1 text-sm text-muted-foreground">
					About how much each person’s My files and each shared folder takes, its trash included.
				</p>
				{space.error ? (
					<Problem error={space.error} onRetry={() => void space.refetch()} />
				) : !space.data ? (
					<p className="mt-4 text-sm text-muted-foreground">Loading…</p>
				) : (
					<div className="mt-4 grid gap-6 lg:grid-cols-2">
						<SpaceList title="People" rows={space.data.people} whole={space.data.used} />
						<SpaceList title="Shared folders" rows={space.data.sharedFolders} whole={space.data.used} empty="No shared folders yet." />
					</div>
				)}
			</section>
		</>
	);
}

function Problem({ error, onRetry }: { error: Error; onRetry: () => void }) {
	return (
		<div className="mt-4 flex items-center gap-3 text-sm">
			<p role="alert" className="text-destructive">
				{error.message}
			</p>
			<Button small onClick={onRetry}>
				Try again
			</Button>
		</div>
	);
}

/** Who switched sharing outside off and when, as a sentence: "Switched off by Réka Kovács on 20 November 2026 at 09:41." */
export function switchedOffBy(s: OutsideSwitch): string {
	const who = s.by ? nameOf(s.by) : s.former ? "someone no longer in the project" : "";
	const when = s.at ? ` on ${formatChangedFull(s.at)}` : "";
	return `Switched off${who ? ` by ${who}` : ""}${when}.`;
}

/** The project's switch of sharing outside the project, with what switching it does, asked before it is switched off. */
function ProjectSwitch({ current }: { current?: OutsideSwitch }) {
	const queryClient = useQueryClient();
	const [asking, setAsking] = useState(false);
	const [busy, setBusy] = useState(false);
	const [problem, setProblem] = useState<string>();
	const change = async (on: boolean) => {
		setBusy(true);
		setProblem(undefined);
		try {
			await api.switchOutside(on);
			setAsking(false);
			for (const key of ["me", "shared-outside", "places", "items", "access", "link"]) void queryClient.invalidateQueries({ queryKey: [key] });
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
		} finally {
			setBusy(false);
		}
	};
	return (
		<section aria-labelledby="outside-switch" className="mt-6 rounded-xl border border-border bg-card p-5">
			<h2 id="outside-switch" className="text-lg font-semibold">
				Sharing outside the project
			</h2>
			{!current ? (
				<p className="mt-1 text-sm text-muted-foreground">Loading…</p>
			) : current.on ? (
				<>
					<p className="mt-1 text-sm">
						On. Members can make public links and invite guests by email address. Managers can switch it off for one shared
						folder.
					</p>
					<Button className="mt-3" onClick={() => setAsking(true)} disabled={busy}>
						Switch it off for the whole project
					</Button>
				</>
			) : (
				<>
					<p className="mt-1 text-sm text-attention">Off for the whole project. {switchedOffBy(current)}</p>
					<p className="mt-1 text-sm text-muted-foreground">
						No public link opens and no guest gets in. Links and guests stay as they are, and work again once it is
						switched back on.
					</p>
					<Button className="mt-3" onClick={() => void change(true)} disabled={busy}>
						Switch it back on
					</Button>
				</>
			)}
			{problem && (
				<p role="alert" className="mt-3 text-sm text-destructive">
					{problem}
				</p>
			)}
			{asking && (
				<Dialog
					title="Switch off sharing outside the project?"
					description="No public link opens and no guest gets in, anywhere in the Drive, until it is switched back on. Nobody can make a link or invite a guest. Links and guests are not removed: they work again once it is switched back on."
					onClose={() => setAsking(false)}
					onSubmit={() => void change(false)}
					footer={
						<>
							<Button variant="ghost" onClick={() => setAsking(false)}>
								Cancel
							</Button>
							<Button variant="primary" type="submit" disabled={busy}>
								Switch it off
							</Button>
						</>
					}
				/>
			)}
		</section>
	);
}

/** Where something shared outside is, as an admin reads it: a shared folder by name, or whose My files. */
function whereOf(s: OutsideShare): string {
	if (s.place.kind === "shared-folder") return s.place.name ?? "A shared folder";
	if (s.place.owner) return `${nameOf(s.place.owner)}’s My files`;
	return "My files of someone no longer in the project";
}

/** Everything shared outside the project, the most recently used first, then the most recently shared. */
function SharedOutsideList({ items }: { items: OutsideShare[] }) {
	const rows = [...items].sort(
		(a, b) => (b.used ?? "").localeCompare(a.used ?? "") || b.since.localeCompare(a.since) || a.item.name.localeCompare(b.item.name),
	);
	return (
		<div className="mt-4 overflow-x-auto rounded-xl border border-border bg-card">
			<table className="w-full min-w-[48rem] table-fixed text-sm">
				<thead>
					<tr className="h-11 border-b border-border text-left text-muted-foreground">
						<th scope="col" className="px-4 font-normal">
							Name
						</th>
						<th scope="col" className="w-44 px-4 font-normal">
							In
						</th>
						<th scope="col" className="w-36 px-4 font-normal">
							Shared by
						</th>
						<th scope="col" className="w-52 px-4 font-normal">
							With
						</th>
						<th scope="col" className="w-36 px-4 font-normal">
							Last used
						</th>
					</tr>
				</thead>
				<tbody>
					{rows.map((s) => (
						<tr key={`${s.kind}-${s.item.id}-${s.guest?.id ?? 0}`} className="border-b border-border last:border-0 align-top">
							<td className="px-4 py-3">
								<div className="flex min-w-0 items-start gap-2">
									<FileIcon item={{ ...s.item }} className="mt-0.5" />
									<div className="min-w-0">
										<p className="font-medium break-words">{s.item.name}</p>
										{!s.works && (
											<p className="text-attention">
												{s.place.outside !== "on" ? "Not working: sharing outside is switched off there" : "Not working now"}
											</p>
										)}
									</div>
								</div>
							</td>
							<td className="px-4 py-3 break-words">{whereOf(s)}</td>
							<td className="px-4 py-3 break-words">{s.sharedBy ? nameOf(s.sharedBy) : "Someone no longer in the project"}</td>
							<td className="px-4 py-3">
								<p className="flex min-w-0 items-start gap-1.5">
									{s.kind === "link" ? (
										<>
											<Link2 aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" /> Anyone with the link
										</>
									) : (
										<>
											<UserRound aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
											<span className="min-w-0 font-mono text-xs leading-5 break-all">{s.guest?.address}</span>
										</>
									)}
								</p>
								<p className="text-muted-foreground">
									{s.role === "editor" ? "Editor" : "Viewer"}
									{s.until ? `, until ${formatChanged(s.until)}` : ""}
								</p>
							</td>
							<td className="px-4 py-3">
								{s.used ? (
									<span title={formatChangedFull(s.used)}>
										{formatChanged(s.used)}, {s.uses === 1 ? "once" : `${s.uses} times`}
									</span>
								) : (
									<span className="text-muted-foreground">Not yet</span>
								)}
							</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

/** The space each person, or each shared folder, uses, the largest first, each with its share of the whole. */
function SpaceList({ title, rows, whole, empty }: { title: string; rows: SpaceUsed[]; whole: number; empty?: string }) {
	return (
		<div className="rounded-xl border border-border bg-card p-4">
			<h3 className="text-sm font-semibold">{title}</h3>
			{rows.length === 0 ? (
				<p className="mt-2 text-sm text-muted-foreground">{empty}</p>
			) : (
				<ul className="mt-2 divide-y divide-border">
					{rows.map((r) => {
						const name = r.person ? nameOf(r.person) : r.former ? `${r.folder} (no longer in the project)` : (r.name ?? "");
						return (
							<li key={r.person?.userId ?? `${r.id ?? r.folder}`} className="py-2 text-sm">
								<div className="flex items-baseline gap-3">
									<span className="min-w-0 flex-1 break-words">
										{name}
										{r.deleted && <span className="text-muted-foreground"> (in the trash)</span>}
									</span>
									<span className="shrink-0 tabular-nums text-muted-foreground">{r.used === 0 ? "Nothing" : formatSize(r.used)}</span>
								</div>
								{whole > 0 && r.used > 0 && (
									<div aria-hidden className="mt-1 h-1 overflow-hidden rounded-full bg-muted">
										<div className="h-full bg-foreground/60" style={{ width: `${Math.max(1, (r.used / whole) * 100)}%` }} />
									</div>
								)}
							</li>
						);
					})}
				</ul>
			)}
		</div>
	);
}

/** How much a held My files holds, as a sentence: "4 files, 22 B", "Empty". */
function holds(files: number, size: number): string {
	if (files === 0) return "No files";
	return `${files === 1 ? "1 file" : `${files} files`}, ${formatSize(size)}`;
}

/** Whose files they were, as a project admin reads it: their address when they first opened the Drive. */
function whose(folder: string) {
	return <span className="font-mono text-xs break-all">{folder}</span>;
}

/** The held My files of people who left, each with what can be done with it, or what is being done. */
function HeldList({ rows }: { rows: Held[] }) {
	const [handing, setHanding] = useState<Held>();
	const [deleting, setDeleting] = useState<Held>();
	return (
		<>
			<ul className="mt-4 divide-y divide-border rounded-xl border border-border bg-card">
				{rows.map((h) => (
					<li key={h.place} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 text-sm">
						<div className="min-w-0 flex-1">
							<p>{whose(h.folder)}</p>
							<p className="text-muted-foreground">
								{holds(h.files, h.size)}
								{h.used > h.size && `, and ${formatSize(h.used - h.size)} in their trash`}
							</p>
						</div>
						{h.act ? (
							<p className="text-attention">
								{h.act.kind === "hand-over"
									? `Being handed over to ${h.act.to ? nameOf(h.act.to) : "someone who has left"}…`
									: "Being deleted…"}
							</p>
						) : (
							<div className="flex gap-2">
								<Button small onClick={() => setHanding(h)}>
									Hand over…
								</Button>
								<Button small variant="danger" onClick={() => setDeleting(h)}>
									Delete…
								</Button>
							</div>
						)}
					</li>
				))}
			</ul>
			{handing && <HandOverDialog held={handing} onClose={() => setHanding(undefined)} />}
			{deleting && <DeleteHeldDialog held={deleting} onClose={() => setDeleting(undefined)} />}
		</>
	);
}

/** Runs a hand-over or deletion, and refreshes what it changes. */
function useAct(onClose: () => void) {
	const queryClient = useQueryClient();
	const [busy, setBusy] = useState(false);
	const [problem, setProblem] = useState<string>();
	const run = async (act: () => Promise<HeldAct>) => {
		setBusy(true);
		setProblem(undefined);
		try {
			await act();
			onClose();
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
		} finally {
			setBusy(false);
			for (const key of ["held", "space", "shared-outside", "places", "items", "usage"]) void queryClient.invalidateQueries({ queryKey: [key] });
		}
	};
	return { busy, problem, run };
}

/** Choosing whom to hand a held My files to, with what that does, before it is done. */
function HandOverDialog({ held, onClose }: { held: Held; onClose: () => void }) {
	const members = useQuery({ queryKey: ["members"], queryFn: api.members });
	const [to, setTo] = useState("");
	const { busy, problem, run } = useAct(onClose);
	return (
		<Dialog
			title="Hand these files to another member?"
			description={
				<>
					The files of {whose(held.folder)} ({holds(held.files, held.size)}) become a folder of their own in the My files of
					the member you choose. Nothing that was shared from them comes along: their links, guests and shares stay ended.
					What they had deleted is removed. It is recorded with your name, and cannot be undone.
				</>
			}
			onClose={onClose}
			onSubmit={() => to && void run(() => api.handOver(held.place, to))}
			footer={
				<>
					<Button variant="ghost" onClick={onClose}>
						Cancel
					</Button>
					<Button variant="primary" type="submit" disabled={busy || !to}>
						Hand over
					</Button>
				</>
			}
		>
			<label className="block text-sm">
				<span className="font-medium">To</span>
				<select
					value={to}
					onChange={(e) => setTo(e.target.value)}
					className="mt-1 h-9 w-full rounded-md border border-input bg-card px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
				>
					<option value="">Choose a member</option>
					{members.data?.map((m) => (
						<option key={m.userId} value={m.userId}>
							{m.displayName ? `${m.displayName} (${m.email})` : m.email}
						</option>
					))}
				</select>
			</label>
			{problem && (
				<p role="alert" className="mt-3 text-sm text-destructive">
					{problem}
				</p>
			)}
		</Dialog>
	);
}

/** Deleting a held My files for good, asked first, saying it cannot be undone. */
function DeleteHeldDialog({ held, onClose }: { held: Held; onClose: () => void }) {
	const { busy, problem, run } = useAct(onClose);
	return (
		<Dialog
			title="Delete these files for good?"
			description={
				<>
					The files of {whose(held.folder)} ({holds(held.files, held.size)}), and what they had deleted, are removed from the
					Drive storage. They do not go to a trash: this cannot be undone. It is recorded with your name.
				</>
			}
			onClose={onClose}
			onSubmit={() => void run(() => api.deleteHeld(held.place))}
			footer={
				<>
					<Button variant="ghost" onClick={onClose}>
						Cancel
					</Button>
					<Button variant="danger" type="submit" disabled={busy}>
						Delete for good
					</Button>
				</>
			}
		>
			{problem && (
				<p role="alert" className="text-sm text-destructive">
					{problem}
				</p>
			)}
		</Dialog>
	);
}

/** The record of hand-overs and deletions, the most recent first: who did it, when, and what. */
function ActRecord({ acts }: { acts: HeldAct[] }) {
	const who = (p: { userId: string; email: string; displayName: string } | undefined, former?: boolean) =>
		p ? nameOf(p) : former ? "someone no longer in the project" : "";
	return (
		<div className="mt-4">
			<h3 className="text-sm font-semibold">Handed over and deleted</h3>
			<ul className="mt-2 divide-y divide-border rounded-xl border border-border bg-card">
				{acts.map((a) => (
					<li key={a.id} className="px-4 py-3 text-sm">
						<p>
							The files of {whose(a.folder)} ({holds(a.files, a.size)}){" "}
							{a.kind === "hand-over" ? (
								<>
									were handed to {who(a.to, a.toFormer)}, as the folder “{a.into}” in their My files,
								</>
							) : (
								"were deleted"
							)}{" "}
							by {who(a.by, a.byFormer)}.
						</p>
						<p className="text-muted-foreground" title={formatChangedFull(a.at)}>
							{formatChangedFull(a.at)}
						</p>
					</li>
				))}
			</ul>
		</div>
	);
}
