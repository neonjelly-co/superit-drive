import { X } from "lucide-react";
import { type ReactNode, useEffect, useId, useRef } from "react";
import { t } from "../i18n";

type Props = {
	title: ReactNode;
	description?: ReactNode;
	/** Called when the person closes the dialog: Escape, the close button, or a click outside it. */
	onClose: () => void;
	children?: ReactNode;
	/** The buttons at the bottom, the main one last. */
	footer: ReactNode;
	/** Called on submit (Enter in a field, or a submit button). */
	onSubmit?: () => void;
	wide?: boolean;
};

/**
 * A modal dialog on the browser's own <dialog>: it keeps focus inside, closes on Escape, and makes the rest of the
 * page inert while it is open. On a phone it sits at the top of the screen and scrolls within it, so the on-screen
 * keyboard never covers its field or its buttons.
 */
export function Dialog({ title, description, onClose, children, footer, onSubmit, wide }: Props) {
	const ref = useRef<HTMLDialogElement>(null);
	const titleId = useId();
	const descriptionId = useId();
	useEffect(() => {
		const dialog = ref.current;
		if (dialog && !dialog.open) dialog.showModal();
		return () => dialog?.close();
	}, []);
	return (
		<dialog
			ref={ref}
			aria-labelledby={titleId}
			aria-describedby={description ? descriptionId : undefined}
			onCancel={(e) => {
				e.preventDefault();
				onClose();
			}}
			onClick={(e) => {
				if (e.target === e.currentTarget) onClose(); // the backdrop
			}}
			className={`m-auto max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] overflow-y-auto overscroll-contain max-md:mt-3 max-md:mb-auto max-md:max-h-[calc(100dvh-1.5rem)] ${wide ? "max-w-lg" : "max-w-md"} rounded-xl border border-border bg-card p-0 text-foreground shadow-xl backdrop:bg-foreground/30`}
		>
			<form
				method="dialog"
				className="p-6 max-md:p-5"
				onSubmit={(e) => {
					e.preventDefault();
					onSubmit?.();
				}}
			>
				<div className="flex items-start gap-3">
					<h2 id={titleId} className="min-w-0 flex-1 text-lg font-semibold break-words">
						{title}
					</h2>
					<button
						type="button"
						onClick={onClose}
						aria-label={t.common.close}
						className="-mt-1 -mr-2 grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground max-md:-mt-2.5 max-md:size-11"
					>
						<X aria-hidden className="size-4" />
					</button>
				</div>
				{description && (
					<p id={descriptionId} className="mt-1 text-sm text-muted-foreground">
						{description}
					</p>
				)}
				{children && <div className="mt-4">{children}</div>}
				<div className="mt-6 flex flex-wrap justify-end gap-2">{footer}</div>
			</form>
		</dialog>
	);
}
