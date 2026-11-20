import { X } from "lucide-react";
import { type ReactNode, useEffect, useRef } from "react";
import { useNarrow } from "../narrow";

type Props = {
	/** What the panel is, for screen readers. */
	label: string;
	/** What its close button says. */
	closeLabel: string;
	onClose: () => void;
	children: ReactNode;
};

/**
 * The details panel's frame: on the right of the list on a wide screen; on a phone, a sheet that slides up from the
 * bottom over the list (a modal dialog: the list behind it is inert, Escape or a tap outside closes it).
 */
export function SidePanel({ label, closeLabel, onClose, children }: Props) {
	const narrow = useNarrow();
	const close = (
		<button
			type="button"
			onClick={onClose}
			aria-label={closeLabel}
			className="absolute top-3 right-3 grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground max-md:-top-2.5 max-md:-right-2.5 max-md:size-11"
		>
			<X aria-hidden className="size-4" />
		</button>
	);
	if (!narrow) {
		return (
			<aside aria-label={label} className="relative w-96 shrink-0 overflow-auto border-l border-border bg-card p-6 pb-24">
				{close}
				{children}
			</aside>
		);
	}
	return (
		<Sheet label={label} onClose={onClose}>
			{close}
			{children}
		</Sheet>
	);
}

function Sheet({ label, onClose, children }: { label: string; onClose: () => void; children: ReactNode }) {
	const ref = useRef<HTMLDialogElement>(null);
	useEffect(() => {
		const dialog = ref.current;
		if (dialog && !dialog.open) dialog.showModal();
		return () => dialog?.close();
	}, []);
	return (
		<dialog
			ref={ref}
			aria-label={label}
			onCancel={(e) => {
				e.preventDefault();
				onClose();
			}}
			onClick={(e) => {
				if (e.target === e.currentTarget) onClose(); // the backdrop
			}}
			className="m-0 mt-auto max-h-[85dvh] w-full max-w-none animate-[sheet-up_200ms_ease-out] overflow-y-auto overscroll-contain rounded-t-2xl border border-b-0 border-border bg-card p-5 pt-3 pb-[max(2rem,env(safe-area-inset-bottom))] text-foreground shadow-xl backdrop:bg-foreground/30 motion-reduce:animate-none"
		>
			<div aria-hidden className="mx-auto mb-4 h-1 w-10 rounded-full bg-border" />
			<div className="relative">{children}</div>
		</dialog>
	);
}
