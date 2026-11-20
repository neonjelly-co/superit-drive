import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link2, UserRound } from "lucide-react";
import { useState } from "react";
import { nameOf } from "../access";
import { api, type Held, type HeldAct, type OutsideShare, type OutsideSwitch, type SpaceUsed } from "../api";
import { formatChanged, formatChangedFull, formatSize } from "../format";
import { fill, t } from "../i18n";
import { useNarrow } from "../narrow";
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
			<h1 className="text-xl font-semibold">{t.app.sharingAndSpace}</h1>
			<p className="mt-1 text-sm text-muted-foreground">{t.oversight.intro}</p>
			<ProjectSwitch current={outside.data?.sharingOutside} />
			<section aria-labelledby="shared-outside" className="mt-8">
				<h2 id="shared-outside" className="text-lg font-semibold">
					{t.oversight.sharedOutside}
				</h2>
				<p className="mt-1 text-sm text-muted-foreground">{t.oversight.sharedOutsideNote}</p>
				{outside.error ? (
					<Problem error={outside.error} onRetry={() => void outside.refetch()} />
				) : !outside.data ? (
					<p className="mt-4 text-sm text-muted-foreground">{t.common.loading}</p>
				) : outside.data.items.length === 0 ? (
					<p className="mt-4 text-sm text-muted-foreground">{t.oversight.nothingOutside}</p>
				) : (
					<SharedOutsideList items={outside.data.items} />
				)}
			</section>
			<section aria-labelledby="held" className="mt-8">
				<h2 id="held" className="text-lg font-semibold">
					{t.oversight.peopleWhoLeft}
				</h2>
				<p className="mt-1 text-sm text-muted-foreground">{t.oversight.peopleWhoLeftNote}</p>
				{held.error ? (
					<Problem error={held.error} onRetry={() => void held.refetch()} />
				) : !held.data ? (
					<p className="mt-4 text-sm text-muted-foreground">{t.common.loading}</p>
				) : (
					<>
						{held.data.held.length === 0 ? (
							<p className="mt-4 text-sm text-muted-foreground">{t.oversight.nobodyLeft}</p>
						) : (
							<HeldList rows={held.data.held} />
						)}
						{held.data.acts.some((a) => a.done) && <ActRecord acts={held.data.acts.filter((a) => a.done)} />}
					</>
				)}
			</section>
			<section aria-labelledby="space" className="mt-8">
				<h2 id="space" className="text-lg font-semibold">
					{t.oversight.spaceUsed}
				</h2>
				<p className="mt-1 text-sm text-muted-foreground">{t.oversight.spaceNote}</p>
				{space.error ? (
					<Problem error={space.error} onRetry={() => void space.refetch()} />
				) : !space.data ? (
					<p className="mt-4 text-sm text-muted-foreground">{t.common.loading}</p>
				) : (
					<div className="mt-4 grid gap-6 lg:grid-cols-2">
						<SpaceList title={t.oversight.people} rows={space.data.people} whole={space.data.used} />
						<SpaceList
							title={t.common.sharedFolders}
							rows={space.data.sharedFolders}
							whole={space.data.used}
							empty={t.oversight.noSharedFolders}
						/>
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
				{t.common.tryAgain}
			</Button>
		</div>
	);
}

/** Who switched sharing outside off and when, as a sentence: "Switched off by Réka Kovács on 20 November 2026 at 09:41." */
export function switchedOffBy(s: OutsideSwitch): string {
	const who = s.by ? nameOf(s.by) : s.former ? t.common.someoneWhoLeftLower : "";
	const when = s.at ? formatChangedFull(s.at) : "";
	if (who && when) return t.oversight.switchedOffByAt(who, when);
	if (who) return t.oversight.switchedOffBy(who);
	if (when) return t.oversight.switchedOffAt(when);
	return t.oversight.switchedOff;
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
				{t.oversight.outsideTitle}
			</h2>
			{!current ? (
				<p className="mt-1 text-sm text-muted-foreground">{t.common.loading}</p>
			) : current.on ? (
				<>
					<p className="mt-1 text-sm">{t.oversight.outsideOn}</p>
					<Button className="mt-3" onClick={() => setAsking(true)} disabled={busy}>
						{t.oversight.switchOffProject}
					</Button>
				</>
			) : (
				<>
					<p className="mt-1 text-sm text-attention">
						{t.oversight.offForProject} {switchedOffBy(current)}
					</p>
					<p className="mt-1 text-sm text-muted-foreground">{t.oversight.offNote}</p>
					<Button className="mt-3" onClick={() => void change(true)} disabled={busy}>
						{t.oversight.switchBackOn}
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
					title={t.oversight.switchOffTitle}
					description={t.oversight.switchOffNote}
					onClose={() => setAsking(false)}
					onSubmit={() => void change(false)}
					footer={
						<>
							<Button variant="ghost" onClick={() => setAsking(false)}>
								{t.common.cancel}
							</Button>
							<Button variant="primary" type="submit" disabled={busy}>
								{t.oversight.switchOff}
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
	if (s.place.kind === "shared-folder") return s.place.name ?? t.oversight.aSharedFolder;
	if (s.place.owner) return t.oversight.myFilesOf(nameOf(s.place.owner));
	return t.oversight.myFilesOfFormer;
}

/** Everything shared outside the project, the most recently used first, then the most recently shared. */
function SharedOutsideList({ items }: { items: OutsideShare[] }) {
	const narrow = useNarrow();
	const rows = [...items].sort(
		(a, b) => (b.used ?? "").localeCompare(a.used ?? "") || b.since.localeCompare(a.since) || a.item.name.localeCompare(b.item.name),
	);
	const keyOf = (s: OutsideShare) => `${s.kind}-${s.item.id}-${s.guest?.id ?? 0}`;
	if (narrow) {
		// On a phone, one card per share, its parts stacked: nothing scrolls sideways.
		return (
			<ul className="mt-4 divide-y divide-border rounded-xl border border-border bg-card text-sm">
				{rows.map((s) => (
					<li key={keyOf(s)} className="px-4 py-3">
						<div className="flex min-w-0 items-start gap-2">
							<FileIcon item={{ ...s.item }} className="mt-0.5" />
							<div className="min-w-0">
								<p className="font-medium break-words">{s.item.name}</p>
								<NotWorking s={s} />
							</div>
						</div>
						<dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
							<dt className="text-muted-foreground">{t.oversight.in}</dt>
							<dd className="min-w-0 break-words">{whereOf(s)}</dd>
							<dt className="text-muted-foreground">{t.shared.sharedBy}</dt>
							<dd className="min-w-0 break-words">{sharedByOf(s)}</dd>
							<dt className="text-muted-foreground">{t.oversight.with}</dt>
							<dd className="min-w-0">
								<With s={s} />
							</dd>
							<dt className="text-muted-foreground">{t.oversight.lastUsed}</dt>
							<dd>
								<LastUsed s={s} />
							</dd>
						</dl>
					</li>
				))}
			</ul>
		);
	}
	return (
		<div className="mt-4 overflow-x-auto rounded-xl border border-border bg-card">
			<table className="w-full min-w-[48rem] table-fixed text-sm">
				<thead>
					<tr className="h-11 border-b border-border text-left text-muted-foreground">
						<th scope="col" className="px-4 font-normal">
							{t.common.name}
						</th>
						<th scope="col" className="w-44 px-4 font-normal">
							{t.oversight.in}
						</th>
						<th scope="col" className="w-36 px-4 font-normal">
							{t.shared.sharedBy}
						</th>
						<th scope="col" className="w-52 px-4 font-normal">
							{t.oversight.with}
						</th>
						<th scope="col" className="w-36 px-4 font-normal">
							{t.oversight.lastUsed}
						</th>
					</tr>
				</thead>
				<tbody>
					{rows.map((s) => (
						<tr key={keyOf(s)} className="border-b border-border last:border-0 align-top">
							<td className="px-4 py-3">
								<div className="flex min-w-0 items-start gap-2">
									<FileIcon item={{ ...s.item }} className="mt-0.5" />
									<div className="min-w-0">
										<p className="font-medium break-words">{s.item.name}</p>
										<NotWorking s={s} />
									</div>
								</div>
							</td>
							<td className="px-4 py-3 break-words">{whereOf(s)}</td>
							<td className="px-4 py-3 break-words">{sharedByOf(s)}</td>
							<td className="px-4 py-3">
								<With s={s} />
							</td>
							<td className="px-4 py-3">
								<LastUsed s={s} />
							</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

const sharedByOf = (s: OutsideShare) => (s.sharedBy ? nameOf(s.sharedBy) : t.common.someoneWhoLeft);

function NotWorking({ s }: { s: OutsideShare }) {
	if (s.works) return null;
	return <p className="text-attention">{s.place.outside !== "on" ? t.oversight.notWorkingOff : t.oversight.notWorking}</p>;
}

/** With whom it is shared: anyone with the link, or a guest by address; and what they may do, until when. */
function With({ s }: { s: OutsideShare }) {
	return (
		<>
			<p className="flex min-w-0 items-start gap-1.5">
				{s.kind === "link" ? (
					<>
						<Link2 aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
						{s.linkKind === "upload-only" ? t.who.anyoneWithUploadLink : t.who.anyoneWithLink}
					</>
				) : (
					<>
						<UserRound aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
						<span className="min-w-0 font-mono text-xs leading-5 break-all">{s.guest?.address}</span>
					</>
				)}
			</p>
			<p className="text-muted-foreground">
				{[
					s.linkKind === "upload-only" ? t.who.canOnlySendIn : s.role === "editor" ? t.common.editor : t.common.viewer,
					...(s.until ? [t.oversight.until(formatChanged(s.until))] : []),
				].join(", ")}
			</p>
		</>
	);
}

function LastUsed({ s }: { s: OutsideShare }) {
	if (!s.used) return <span className="text-muted-foreground">{t.oversight.notYet}</span>;
	return (
		<span title={formatChangedFull(s.used)}>
			{s.uses === 1 ? t.oversight.usedOnce(formatChanged(s.used)) : t.oversight.usedTimes(formatChanged(s.used), s.uses)}
		</span>
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
						const name = r.person ? nameOf(r.person) : r.former ? t.oversight.noLongerInProject(r.folder ?? "") : (r.name ?? "");
						return (
							<li key={r.person?.userId ?? `${r.id ?? r.folder}`} className="py-2 text-sm">
								<div className="flex items-baseline gap-3">
									<span className="min-w-0 flex-1 break-words">
										{name}
										{r.deleted && <span className="text-muted-foreground"> {t.oversight.inTheTrash}</span>}
									</span>
									<span className="shrink-0 tabular-nums text-muted-foreground">
										{r.used === 0 ? t.oversight.nothing : formatSize(r.used)}
									</span>
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

/** How much a held My files holds, as a few words: "4 files, 22 B", "No files". */
function holds(files: number, size: number): string {
	if (files === 0) return t.oversight.noFiles;
	return t.oversight.files(files, formatSize(size));
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
								{[holds(h.files, h.size), ...(h.used > h.size ? [t.oversight.inTheirTrash(formatSize(h.used - h.size))] : [])].join(
									", ",
								)}
							</p>
						</div>
						{h.act ? (
							<p className="text-attention">
								{h.act.kind === "hand-over"
									? h.act.to
										? t.oversight.handingOver(nameOf(h.act.to))
										: t.oversight.handingOverToFormer
									: t.oversight.deleting}
							</p>
						) : (
							<div className="flex gap-2">
								<Button small onClick={() => setHanding(h)}>
									{t.oversight.handOverButton}
								</Button>
								<Button small variant="danger" onClick={() => setDeleting(h)}>
									{t.oversight.deleteButton}
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
			title={t.oversight.handOverTitle}
			description={fill(t.oversight.handOverNote, { whose: whose(held.folder), holds: holds(held.files, held.size) })}
			onClose={onClose}
			onSubmit={() => to && void run(() => api.handOver(held.place, to))}
			footer={
				<>
					<Button variant="ghost" onClick={onClose}>
						{t.common.cancel}
					</Button>
					<Button variant="primary" type="submit" disabled={busy || !to}>
						{t.oversight.handOver}
					</Button>
				</>
			}
		>
			<label className="block text-sm">
				<span className="font-medium">{t.oversight.to}</span>
				<select
					value={to}
					onChange={(e) => setTo(e.target.value)}
					className="mt-1 h-9 w-full rounded-md border border-input bg-card px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 max-md:h-11 max-md:text-base"
				>
					<option value="">{t.oversight.chooseMember}</option>
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
			title={t.oversight.deleteTitle}
			description={fill(t.oversight.deleteNote, { whose: whose(held.folder), holds: holds(held.files, held.size) })}
			onClose={onClose}
			onSubmit={() => void run(() => api.deleteHeld(held.place))}
			footer={
				<>
					<Button variant="ghost" onClick={onClose}>
						{t.common.cancel}
					</Button>
					<Button variant="danger" type="submit" disabled={busy}>
						{t.oversight.deleteForGood}
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
		p ? nameOf(p) : former ? t.common.someoneWhoLeftLower : "";
	return (
		<div className="mt-4">
			<h3 className="text-sm font-semibold">{t.oversight.record}</h3>
			<ul className="mt-2 divide-y divide-border rounded-xl border border-border bg-card">
				{acts.map((a) => (
					<li key={a.id} className="px-4 py-3 text-sm">
						<p>
							{fill(a.kind === "hand-over" ? t.oversight.recordHandedOver : t.oversight.recordDeleted, {
								whose: whose(a.folder),
								holds: holds(a.files, a.size),
								to: who(a.to, a.toFormer),
								into: a.into ?? "",
								by: who(a.by, a.byFormer),
							})}
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
