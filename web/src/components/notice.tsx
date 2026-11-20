import { X } from "lucide-react";
import { useEffect } from "react";
import { t } from "../i18n";
import { Button } from "./button";

/** Something the Drive just did, said once, with what can be done about it now (such as Undo). */
export type Notice = {
	key: number;
	text: string;
	/** True when it says something went wrong. */
	failed?: boolean;
	action?: { label: string; run: () => void };
};

/**
 * A notice at the bottom left of the list (across the bottom on a phone). It goes by itself after a while, or when the
 * person closes it.
 */
export function NoticeBar({ notice, onClose }: { notice: Notice | null; onClose: () => void }) {
	useEffect(() => {
		if (!notice) return;
		const timer = setTimeout(onClose, notice.failed ? 15_000 : 8_000);
		return () => clearTimeout(timer);
	}, [notice, onClose]);
	if (!notice) return null;
	return (
		<div
			role="status"
			className="absolute bottom-6 left-6 z-20 flex max-w-[calc(100%-28rem)] min-w-72 items-center gap-3 rounded-xl border border-border bg-card py-2 pr-2 pl-4 text-sm shadow-lg max-md:inset-x-3 max-md:bottom-3 max-md:max-w-none max-md:min-w-0"
		>
			<p className={`min-w-0 flex-1 ${notice.failed ? "text-destructive" : ""}`}>{notice.text}</p>
			{notice.action && (
				<Button
					small
					variant="ghost"
					onClick={() => {
						notice.action?.run();
						onClose();
					}}
				>
					{notice.action.label}
				</Button>
			)}
			<button
				type="button"
				onClick={onClose}
				aria-label={t.common.close}
				className="grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground max-md:size-11"
			>
				<X aria-hidden className="size-4" />
			</button>
		</div>
	);
}
