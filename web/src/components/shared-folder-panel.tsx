import { useQuery, useQueryClient } from "@tanstack/react-query";
import { FolderOpen, Trash2, User, X } from "lucide-react";
import { useId, useState } from "react";
import { api, type Person, type Place } from "../api";
import { Button } from "./button";
import { Dialog } from "./dialog";

type SharedFolder = Extract<Place, { kind: "shared-folder" }>;

type Props = {
	place: SharedFolder;
	onClose: () => void;
	/** Deletes the shared folder; resolves when done, rejects with an Error whose message is for the person. */
	onDelete: () => Promise<unknown>;
};

const ROLE: Record<Person["role"], string> = { viewer: "Viewer", editor: "Editor", manager: "Manager" };

const nameOf = (p: { displayName?: string; email?: string }) => p.displayName || p.email || "";

/**
 * The details panel for a shared folder itself, opened from "Who can see this" beside its name: who has access to it
 * and in which role, making another member a manager, and deleting it. Its managers decide; project admins are
 * managers of every shared folder.
 */
export function SharedFolderPanel({ place, onClose, onDelete }: Props) {
	const queryClient = useQueryClient();
	const canManage = place.can.includes("manage-access");
	const canDelete = place.can.includes("delete-shared-folder");
	const access = useQuery({ queryKey: ["access", place.id], queryFn: () => api.access(place.id) });
	const members = useQuery({ queryKey: ["members"], queryFn: api.members, enabled: canManage });
	const [chosen, setChosen] = useState("");
	const [problem, setProblem] = useState<string>();
	const [busy, setBusy] = useState(false);
	const [confirming, setConfirming] = useState(false);
	const pickerId = useId();

	const change = async (run: () => Promise<Person[]>) => {
		setBusy(true);
		setProblem(undefined);
		try {
			queryClient.setQueryData(["access", place.id], await run());
			setChosen("");
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
			void access.refetch();
		} finally {
			setBusy(false);
			// Someone's role changed, perhaps the person's own: what they can open and do follows.
			void queryClient.invalidateQueries({ queryKey: ["places"] });
		}
	};

	const managing = new Set(access.data?.filter((p) => p.role === "manager").map((p) => p.userId));
	const candidates = members.data?.filter((m) => !managing.has(m.userId)) ?? [];

	return (
		<aside aria-label="Shared folder" className="relative w-96 shrink-0 overflow-auto border-l border-border bg-card p-6 pb-24">
			<button
				type="button"
				onClick={onClose}
				aria-label="Close details"
				className="absolute top-4 right-4 grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
			>
				<X aria-hidden className="size-4" />
			</button>
			<div className="flex items-start gap-3 pr-8">
				<FolderOpen aria-hidden className="mt-0.5 size-6 shrink-0 text-muted-foreground" />
				<div className="min-w-0">
					<h2 className="text-lg font-semibold break-words">{place.name}</h2>
					<p className="text-sm text-muted-foreground">Shared folder, owned by the project</p>
				</div>
			</div>

			<h3 className="mt-6 text-sm font-semibold">Who can see this</h3>
			<p className="mt-0.5 text-sm text-muted-foreground">
				Its managers decide who has access to it and can delete it. Project admins are managers of every shared folder.
			</p>
			{access.error ? (
				<p className="mt-2 text-sm text-destructive">{access.error.message}</p>
			) : !access.data ? (
				<p className="mt-2 text-sm text-muted-foreground">Loading…</p>
			) : (
				<ul className="mt-2 text-sm">
					{access.data.map((p) => (
						<li key={p.userId} className="flex items-center gap-2.5 border-b border-border py-2 last:border-0">
							<User aria-hidden className="size-4 shrink-0 text-muted-foreground" />
							<div className="min-w-0 flex-1">
								<p className="truncate">{p.former ? "Someone no longer in the project" : nameOf(p)}</p>
								<p className="truncate text-muted-foreground">
									{ROLE[p.role]}
									{p.admin ? " · project admin" : ""}
									{!p.former && p.displayName && p.email ? ` · ${p.email}` : ""}
								</p>
							</div>
							{canManage && p.given && !p.admin && (
								<Button
									small
									variant="ghost"
									aria-label={`Take away ${p.former ? "this role" : `${nameOf(p)}’s role`}`}
									title="Take away"
									disabled={busy}
									onClick={() => void change(() => api.takeAccess(place.id, p.userId))}
								>
									<X aria-hidden />
								</Button>
							)}
						</li>
					))}
				</ul>
			)}

			{canManage && (
				<form
					className="mt-6"
					onSubmit={(e) => {
						e.preventDefault();
						if (chosen) void change(() => api.giveAccess(place.id, chosen, "manager"));
					}}
				>
					<label htmlFor={pickerId} className="text-sm font-semibold">
						Make someone a manager
					</label>
					{members.data && candidates.length === 0 ? (
						<p className="mt-1 text-sm text-muted-foreground">Everyone in the project already manages it.</p>
					) : (
						<div className="mt-2 flex gap-2">
							<select
								id={pickerId}
								value={chosen}
								onChange={(e) => setChosen(e.target.value)}
								className="h-9 min-w-0 flex-1 rounded-md border border-input bg-card px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
							>
								<option value="">{members.isPending ? "Loading…" : "Choose a member"}</option>
								{candidates.map((m) => (
									<option key={m.userId} value={m.userId}>
										{m.displayName ? `${m.displayName} (${m.email})` : m.email}
									</option>
								))}
							</select>
							<Button type="submit" disabled={busy || !chosen}>
								Add
							</Button>
						</div>
					)}
					<p className="mt-2 text-sm text-muted-foreground">
						A manager can do everything in it, decide who has access, and delete it.
					</p>
				</form>
			)}
			{problem && (
				<p role="alert" className="mt-3 text-sm text-destructive">
					{problem}
				</p>
			)}

			{canDelete && (
				<div className="mt-8 border-t border-border pt-5">
					<Button variant="danger" onClick={() => setConfirming(true)} disabled={busy}>
						<Trash2 aria-hidden /> Delete shared folder
					</Button>
				</div>
			)}
			{confirming && <ConfirmDelete name={place.name} onDelete={onDelete} onClose={() => setConfirming(false)} />}
		</aside>
	);
}

function ConfirmDelete({ name, onDelete, onClose }: { name: string; onDelete: () => Promise<unknown>; onClose: () => void }) {
	const [problem, setProblem] = useState<string>();
	const [busy, setBusy] = useState(false);
	const submit = async () => {
		setBusy(true);
		setProblem(undefined);
		try {
			await onDelete();
			onClose();
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
			setBusy(false);
		}
	};
	return (
		<Dialog
			title={`Delete “${name}”?`}
			description="The shared folder goes to the trash with everything in it, and nobody can open it there. Its managers and project admins can restore it from the trash for 30 days; then it is removed for good."
			onClose={onClose}
			onSubmit={submit}
			footer={
				<>
					<Button variant="ghost" onClick={onClose}>
						Cancel
					</Button>
					<Button variant="danger" type="submit" disabled={busy}>
						<Trash2 aria-hidden /> Delete shared folder
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
