import { X } from "lucide-react";
import { isGoing, type Upload } from "../uploads";
import { FileIcon } from "./file-icon";

/**
 * A small panel in the corner of the list that shows each upload: waiting (in line here, or for its turn at the
 * Drive), waiting for the person's answer about a name that is taken, how far along, trying again after the
 * connection dropped, uploaded (or replaced), skipped, cancelled, or why not. Each one still on its way can be
 * cancelled. It stays until the person closes it, so a refusal is not missed; it can be closed once nothing is on its
 * way.
 */
export function UploadsPanel({
	uploads,
	onCancel,
	onClose,
}: { uploads: Upload[]; onCancel: (key: number) => void; onClose: () => void }) {
	if (uploads.length === 0) return null;
	const going = uploads.filter(isGoing).length;
	const done = uploads.filter((u) => u.state === "done").length;
	const failed = uploads.filter((u) => u.state === "failed").length;
	const skipped = uploads.filter((u) => u.state === "skipped" || u.state === "cancelled").length;

	let summary: string;
	if (going > 0) summary = `Uploading ${going} ${going === 1 ? "item" : "items"}`;
	else if (failed === 0 && skipped === 0) summary = `${done} ${done === 1 ? "item" : "items"} uploaded`;
	else if (failed === 0) summary = `${done} uploaded, ${skipped} not`;
	else if (done === 0) summary = `${failed} could not be uploaded`;
	else summary = `${done} uploaded, ${failed} could not be`;

	return (
		<section
			aria-label="Uploads"
			className="absolute right-6 bottom-6 flex max-h-[60%] w-96 flex-col overflow-hidden rounded-xl border border-border bg-card shadow-lg"
		>
			<header className="flex h-11 shrink-0 items-center gap-2 border-b border-border px-4 text-sm">
				<h2 aria-live="polite" className="flex-1 font-semibold">
					{summary}
				</h2>
				{going === 0 && (
					<button
						type="button"
						onClick={onClose}
						aria-label="Close uploads"
						className="-mr-2 grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
					>
						<X aria-hidden className="size-4" />
					</button>
				)}
			</header>
			<ul className="overflow-auto">
				{uploads.map((u) => (
					<Row key={u.key} upload={u} onCancel={() => onCancel(u.key)} />
				))}
			</ul>
		</section>
	);
}

function Row({ upload: u, onCancel }: { upload: Upload; onCancel: () => void }) {
	const percent = u.size > 0 ? Math.min(100, Math.floor((u.sent / u.size) * 100)) : 0;
	const status = {
		waiting: u.turn ? "Waiting for its turn" : "Waiting",
		asking: "Waiting for you",
		uploading: `${percent}%`,
		reconnecting: "Connection lost, trying again",
		done: u.kind === "folder" ? "Made" : u.existing === "replace" ? "Replaced" : "Uploaded",
		failed: "Not uploaded",
		skipped: "Skipped",
		cancelled: "Cancelled",
	}[u.state];
	const label = u.where ? `${u.where} › ${u.name}` : u.name;
	return (
		<li className="border-b border-border px-4 py-2.5 text-sm last:border-0">
			<div className="flex items-center gap-2.5">
				<FileIcon item={{ name: u.name, kind: u.kind }} className="size-4" />
				<span className="min-w-0 flex-1 truncate" title={label}>
					{u.where && <span className="text-muted-foreground">{u.where} › </span>}
					{u.name}
				</span>
				<span className={`shrink-0 tabular-nums ${u.state === "failed" ? "text-destructive" : "text-muted-foreground"}`}>
					{status}
				</span>
				{isGoing(u) && u.state !== "asking" && (
					<button
						type="button"
						onClick={onCancel}
						aria-label={`Cancel the upload of ${u.name}`}
						title="Cancel"
						className="-mr-1.5 grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
					>
						<X aria-hidden className="size-3.5" />
					</button>
				)}
			</div>
			{(u.state === "uploading" || u.state === "reconnecting") && (
				<div
					role="progressbar"
					aria-label={`Uploading ${u.name}`}
					aria-valuenow={percent}
					aria-valuemin={0}
					aria-valuemax={100}
					className="mt-2 h-1 overflow-hidden rounded-full bg-muted"
				>
					<div className="h-full bg-foreground/60 transition-[width]" style={{ width: `${percent}%` }} />
				</div>
			)}
			{u.note && <p className="mt-1 text-muted-foreground">{u.note}</p>}
			{u.message && <p className="mt-1 text-destructive">{u.message}</p>}
		</li>
	);
}
