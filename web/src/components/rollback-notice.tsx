import { History, X } from "lucide-react";
import { useState } from "react";
import type { Rollback } from "../api";

// English for now, as the rest of the interface; Hungarian comes with its own ticket.

const dayOnly = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "long" });
const dayAndYear = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "long", year: "numeric" });
const timeOfDay = new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit" });

/** "18 November", with the year when it is not this one. */
function day(iso: string, now = new Date()): string {
	const d = new Date(iso);
	return (d.getFullYear() === now.getFullYear() ? dayOnly : dayAndYear).format(d);
}

/** Where this browser keeps that the notice of a rollback (by its time) was closed. */
const CLOSED = "superit-drive.rollback-notice-closed";

function closedHere(at: string): boolean {
	try {
		return window.localStorage.getItem(CLOSED) === at;
	} catch {
		return false;
	}
}

function closeHere(at: string) {
	try {
		window.localStorage.setItem(CLOSED, at);
	} catch {
		// Without storage it is closed until the page is loaded again.
	}
}

/**
 * For seven days after the Drive storage was put back to a snapshot, every member sees who did it and to when, so that
 * they understand why recent files are gone. It can be closed: then it stays closed in this browser, for this rollback,
 * and nowhere else. The name is the platform's text, shown as text.
 */
export function RollbackNotice({ rollback }: { rollback: Rollback }) {
	const [closed, setClosed] = useState(() => closedHere(rollback.at));
	if (closed) return null;
	const to = `${day(rollback.to)} at ${timeOfDay.format(new Date(rollback.to))}`;
	return (
		<div role="status" className="mb-5 flex items-start gap-3 rounded-xl border border-border bg-card py-3 pr-2 pl-4 text-sm">
			<History aria-hidden className="mt-0.5 size-4 shrink-0 text-attention" />
			<div className="min-w-0 flex-1">
				<p className="font-semibold">The files were put back to {to}</p>
				<p className="mt-1 break-words text-muted-foreground">
					{rollback.by} put the Drive’s files back to how they were then, on {day(rollback.at)}. Files added after that are
					gone, changes made after that are undone, and files deleted after that are back. Files that came back may need
					to be shared again.
				</p>
			</div>
			<button
				type="button"
				onClick={() => {
					closeHere(rollback.at);
					setClosed(true);
				}}
				aria-label="Close"
				className="grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
			>
				<X aria-hidden className="size-4" />
			</button>
		</div>
	);
}
