import { FolderOpen, Trash2, X } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, type Place } from "../api";
import { AccessSection } from "./access-section";
import { Button } from "./button";
import { Dialog } from "./dialog";
import { switchedOffBy } from "./oversight-view";

type SharedFolder = Extract<Place, { kind: "shared-folder" }>;

type Props = {
	place: SharedFolder;
	/** The person asking. */
	me?: string;
	onClose: () => void;
	/** Deletes the shared folder; resolves when done, rejects with an Error whose message is for the person. */
	onDelete: () => Promise<unknown>;
	/** Called after who has access changed, so the list and the places follow it. */
	onAccessChanged: () => void;
};

/**
 * The details panel for a shared folder itself, opened from "Who can see this" beside its name: who has access to it
 * as a whole and in which role, giving it (viewer, editor or manager, to a member; viewer or editor to everyone in the
 * project), switching sharing outside the project off for it (public links and guests), and deleting it. Its managers
 * decide; project admins are managers of every shared folder.
 */
export function SharedFolderPanel({ place, me, onClose, onDelete, onAccessChanged }: Props) {
	const canDelete = place.can.includes("delete-shared-folder");
	const [confirming, setConfirming] = useState(false);

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

			<AccessSection
				placeId={place.id}
				itemId={null}
				what="shared folder"
				canChange={place.can.includes("manage-access")}
				roles={["viewer", "editor", "manager"]}
				me={me}
				onChanged={onAccessChanged}
			/>

			<OutsideSwitch place={place} onChanged={onAccessChanged} />

			{canDelete && (
				<div className="mt-8 border-t border-border pt-5">
					<Button variant="danger" onClick={() => setConfirming(true)}>
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

/**
 * Sharing outside the project for this shared folder: on or off, and, for its managers, switching it. While it is off
 * for the whole project, it is off here whatever this switch says.
 */
function OutsideSwitch({ place, onChanged }: { place: SharedFolder; onChanged: () => void }) {
	const queryClient = useQueryClient();
	const [busy, setBusy] = useState(false);
	const [problem, setProblem] = useState<string>();
	const canSwitch = place.can.includes("switch-sharing-outside");
	const own = place.sharingOutside;
	const change = async (on: boolean) => {
		setBusy(true);
		setProblem(undefined);
		try {
			await api.switchFolderOutside(place.id, on);
			await queryClient.invalidateQueries({ queryKey: ["places"] });
			void queryClient.invalidateQueries({ queryKey: ["link", place.id] });
			onChanged();
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
		} finally {
			setBusy(false);
		}
	};
	return (
		<section aria-labelledby={`outside-${place.id}`} className="mt-6">
			<h3 id={`outside-${place.id}`} className="text-sm font-semibold">
				Sharing outside the project
			</h3>
			{own.on ? (
				<p className="mt-0.5 text-sm text-muted-foreground">
					On for this shared folder: whoever may share something in it can make a public link or invite a guest.
				</p>
			) : (
				<p className="mt-0.5 text-sm text-attention">
					Off for this shared folder: no public link in it opens and no guest gets in. {switchedOffBy(own)}
				</p>
			)}
			{place.outside === "project" && (
				<p className="mt-1 text-sm text-attention">A project admin has switched it off for the whole project, so it is off here too.</p>
			)}
			{canSwitch && (
				<Button className="mt-3" disabled={busy} onClick={() => void change(!own.on)}>
					{own.on ? "Switch it off for this shared folder" : "Switch it back on"}
				</Button>
			)}
			{canSwitch && own.on && (
				<p className="mt-2 text-sm text-muted-foreground">
					Links and guests are not removed: they work again once it is switched back on.
				</p>
			)}
			{problem && (
				<p role="alert" className="mt-2 text-sm text-destructive">
					{problem}
				</p>
			)}
		</section>
	);
}
