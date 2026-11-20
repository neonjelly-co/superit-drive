import { MoreHorizontal } from "lucide-react";
import { type PointerEvent, type ReactNode, useRef } from "react";
import { api, type Item } from "../api";
import { FileIcon } from "./file-icon";

/** How long a finger stays on a row before it counts as press and hold. */
const HOLD = 500;

/**
 * Press and hold: handlers for an element that call onHold when a finger (or the mouse) stays on it, without moving,
 * for half a second. The click that ends that press does nothing else (also once the row has changed under the finger,
 * so the handlers stay when onHold goes), and the browser's own long-press menu is not shown.
 */
export function useHold(onHold: (() => void) | undefined) {
	const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
	const start = useRef<{ x: number; y: number }>(undefined);
	const held = useRef(false);
	const stop = () => {
		clearTimeout(timer.current);
		timer.current = undefined;
	};
	return {
		onPointerDown: (e: PointerEvent) => {
			if (e.button !== 0 || !onHold) return;
			held.current = false;
			start.current = { x: e.clientX, y: e.clientY };
			stop();
			timer.current = setTimeout(() => {
				held.current = true;
				onHold();
			}, HOLD);
		},
		onPointerMove: (e: PointerEvent) => {
			const s = start.current;
			if (s && Math.hypot(e.clientX - s.x, e.clientY - s.y) > 10) stop();
		},
		onPointerUp: stop,
		onPointerCancel: stop,
		onPointerLeave: stop,
		onContextMenu: (e: { preventDefault: () => void }) => e.preventDefault(),
		onClickCapture: (e: { preventDefault: () => void; stopPropagation: () => void }) => {
			if (!held.current) return;
			held.current = false;
			e.preventDefault();
			e.stopPropagation();
		},
	};
}

type Props = {
	/** The file or folder: its icon and name. */
	item: { name: string; kind: "file" | "folder" | "shared-folder" };
	/** The one line under the name: who can see it and its size, where it is, who shared it. */
	line: ReactNode;
	/** What tapping the name does: a file's address (it opens or downloads), or a folder's opening. */
	open?: { placeId: number; file: Item } | { folder: () => void };
	/** Shows its details (the sheet), from the ⋯ button. */
	onDetails?: () => void;
	detailsLabel?: string;
	/** While choosing several: whether it is ticked, and what tapping anywhere on the row does. */
	selecting?: boolean;
	ticked?: boolean;
	onTick?: () => void;
	tickLabel?: string;
	/** Press and hold: starts choosing several, with this row ticked. */
	onHold?: () => void;
	/** Anything else at the end of the row, such as Restore. */
	end?: ReactNode;
	/** True when the line under the name may take two lines (it says what matters, such as when it is removed). */
	longLine?: boolean;
};

/**
 * One row of a list on a phone: the icon and the name, with one line under it, then the ⋯ button for its details. The
 * name (and its line) is one target as tall as the row: a file opens or downloads, a folder opens. While choosing
 * several, the row shows its tick box and a tap anywhere ticks it. Every target is at least 44 px.
 */
export function PhoneRow({ item, line, open, onDetails, detailsLabel, selecting, ticked, onTick, tickLabel, onHold, end, longLine }: Props) {
	const hold = useHold(selecting ? undefined : onHold);
	const body = (
		<>
			<FileIcon item={item} />
			<span className="min-w-0 flex-1">
				<span className="block truncate font-medium">{item.name}</span>
				<span className={`block text-xs text-muted-foreground ${longLine ? "line-clamp-2" : "truncate"}`}>{line}</span>
			</span>
		</>
	);
	const target = "flex min-h-14 min-w-0 flex-1 items-center gap-3 py-2 text-left outline-none focus-visible:bg-muted";
	let opener: ReactNode;
	if (selecting) {
		opener = (
			<button type="button" onClick={onTick} aria-pressed={ticked} className={target}>
				{body}
			</button>
		);
	} else if (open && "file" in open) {
		const f = open.file;
		opener = (
			<a
				href={f.opens ? api.openUrl(open.placeId, f.id) : api.downloadUrl(open.placeId, f.id)}
				{...(f.opens ? { target: "_blank", rel: "noopener noreferrer" } : { download: true })}
				className={target}
			>
				{body}
			</a>
		);
	} else if (open) {
		opener = (
			<button type="button" onClick={open.folder} className={target}>
				{body}
			</button>
		);
	} else {
		opener = <span className={target}>{body}</span>;
	}
	return (
		<li
			{...hold}
			className={`flex items-center gap-1 pr-1 pl-1 [-webkit-touch-callout:none] select-none ${ticked ? "bg-muted" : ""} ${selecting ? "" : "pl-3"}`}
		>
			{selecting && (
				<label className="grid size-11 shrink-0 place-items-center">
					<input
						type="checkbox"
						aria-label={tickLabel}
						checked={ticked ?? false}
						onChange={onTick}
						className="size-5 accent-primary"
					/>
				</label>
			)}
			{opener}
			{!selecting && end}
			{!selecting && onDetails && (
				<button
					type="button"
					onClick={onDetails}
					aria-label={detailsLabel}
					className="grid size-11 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted"
				>
					<MoreHorizontal aria-hidden className="size-5" />
				</button>
			)}
		</li>
	);
}

/** A list of PhoneRows, in a card. */
export function PhoneList({ children, label }: { children: ReactNode; label?: string }) {
	return (
		<ul aria-label={label} className="mt-4 divide-y divide-border overflow-hidden rounded-xl border border-border bg-card">
			{children}
		</ul>
	);
}

/** A message in place of a list's rows: loading, empty, or why it could not be listed. */
export function PhoneMessage({ children }: { children: ReactNode }) {
	return <div className="mt-4 flex flex-col items-center rounded-xl border border-border bg-card px-4 py-10 text-center text-sm text-muted-foreground">{children}</div>;
}
