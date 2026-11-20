import { X } from "lucide-react";
import { t } from "../i18n";
import { isGoing, type Upload } from "../uploads";
import { FileIcon } from "./file-icon";

/**
 * A small panel in the corner of the list that shows each upload: waiting (in line here, or for its turn at the
 * Drive), waiting for the person's answer about a name that is taken, how far along, trying again after the
 * connection dropped, uploaded (or replaced), skipped, cancelled, or why not. Each one still on its way can be
 * cancelled. It stays until the person closes it, so a refusal is not missed; it can be closed once nothing is on its
 * way. On a phone it spans the bottom of the screen (above the bar of what to do with the items selected, `raised`).
 */
export function UploadsPanel({
	uploads,
	onCancel,
	onClose,
	raised,
}: { uploads: Upload[]; onCancel: (key: number) => void; onClose: () => void; raised?: boolean }) {
	if (uploads.length === 0) return null;
	const going = uploads.filter(isGoing).length;
	const done = uploads.filter((u) => u.state === "done").length;
	const failed = uploads.filter((u) => u.state === "failed").length;
	const skipped = uploads.filter((u) => u.state === "skipped" || u.state === "cancelled").length;

	let summary: string;
	if (going > 0) summary = t.uploads.uploading(going);
	else if (failed === 0 && skipped === 0) summary = t.uploads.uploaded(done);
	else if (failed === 0) summary = t.uploads.someSkipped(done, skipped);
	else if (done === 0) summary = t.uploads.noneUploaded(failed);
	else summary = t.uploads.someFailed(done, failed);

	return (
		<section
			aria-label={t.uploads.label}
			className={`absolute right-6 bottom-6 z-20 flex max-h-[60%] w-96 flex-col overflow-hidden rounded-xl border border-border bg-card shadow-lg max-md:inset-x-3 max-md:w-auto max-md:max-h-[45%] ${raised ? "max-md:bottom-36" : "max-md:bottom-3"}`}
		>
			<header className="flex h-11 shrink-0 items-center gap-2 border-b border-border px-4 text-sm">
				<h2 aria-live="polite" className="flex-1 font-semibold">
					{summary}
				</h2>
				{going === 0 && (
					<button
						type="button"
						onClick={onClose}
						aria-label={t.uploads.close}
						className="-mr-2 grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground max-md:size-11"
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
		waiting: u.turn ? t.uploads.waitingTurn : t.uploads.waiting,
		asking: t.uploads.asking,
		uploading: `${percent}%`,
		reconnecting: t.uploads.reconnecting,
		done: u.kind === "folder" ? t.uploads.made : u.existing === "replace" ? t.uploads.replaced : t.uploads.done,
		failed: t.uploads.failed,
		skipped: t.uploads.skipped,
		cancelled: t.uploads.cancelled,
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
						aria-label={t.uploads.cancelNamed(u.name)}
						title={t.common.cancel}
						className="-mr-1.5 grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground max-md:size-11"
					>
						<X aria-hidden className="size-3.5" />
					</button>
				)}
			</div>
			{(u.state === "uploading" || u.state === "reconnecting") && (
				<div
					role="progressbar"
					aria-label={t.uploads.progressNamed(u.name)}
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
