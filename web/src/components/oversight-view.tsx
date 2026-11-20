import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link2, UserRound } from "lucide-react";
import { useState } from "react";
import { nameOf } from "../access";
import { api, type OutsideShare, type OutsideSwitch, type SpaceUsed } from "../api";
import { formatChanged, formatChangedFull, formatSize } from "../format";
import { Button } from "./button";
import { Dialog } from "./dialog";
import { FileIcon } from "./file-icon";

/**
 * "Sharing and space", for a project admin: the switch of sharing outside the project for the whole project, everything
 * shared outside it (public links and guests: the item's name, where it is, who shared it, with whom, when last used and
 * how often, and whether it works now), and how much space each person and each shared folder uses. Names of items
 * shared outside the project, and sums of sizes: never what is in anything.
 */
export function OversightView() {
	const outside = useQuery({ queryKey: ["shared-outside"], queryFn: api.sharedOutside });
	const space = useQuery({ queryKey: ["space"], queryFn: api.space });
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
