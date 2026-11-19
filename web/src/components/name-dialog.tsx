import { useEffect, useId, useRef, useState } from "react";
import { composed, nameProblem } from "../names";
import { Button } from "./button";
import { Dialog } from "./dialog";

type Props = {
	title: string;
	description?: string;
	/** The name the field starts with. */
	initial: string;
	/** The part of it selected at first: a file's name without its extension, so typing keeps the extension. */
	selectStem?: boolean;
	submitLabel: string;
	/** Does the change; resolves when it is done, rejects with an Error whose message is for the person. */
	onSubmit: (name: string) => Promise<unknown>;
	onClose: () => void;
};

/** Asks for a name: for a new folder, or to rename something. The Drive's refusal shows under the field. */
export function NameDialog({ title, description, initial, selectStem, submitLabel, onSubmit, onClose }: Props) {
	const [name, setName] = useState(initial);
	const [problem, setProblem] = useState<string>();
	const [busy, setBusy] = useState(false);
	const input = useRef<HTMLInputElement>(null);
	const fieldId = useId();
	const problemId = useId();

	useEffect(() => {
		const el = input.current;
		if (!el) return;
		el.focus();
		const dot = initial.lastIndexOf(".");
		el.setSelectionRange(0, selectStem && dot > 0 ? dot : initial.length);
	}, [initial, selectStem]);

	const submit = async () => {
		const wanted = composed(name);
		const local = nameProblem(wanted);
		if (local) {
			setProblem(local);
			return;
		}
		setBusy(true);
		setProblem(undefined);
		try {
			await onSubmit(wanted);
			onClose();
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
			setBusy(false);
		}
	};

	return (
		<Dialog
			title={title}
			description={description}
			onClose={onClose}
			onSubmit={submit}
			footer={
				<>
					<Button variant="ghost" onClick={onClose}>
						Cancel
					</Button>
					<Button variant="primary" type="submit" disabled={busy}>
						{submitLabel}
					</Button>
				</>
			}
		>
			<label htmlFor={fieldId} className="text-sm font-medium">
				Name
			</label>
			<input
				ref={input}
				id={fieldId}
				value={name}
				onChange={(e) => {
					setName(e.target.value);
					setProblem(undefined);
				}}
				aria-invalid={problem ? true : undefined}
				aria-describedby={problem ? problemId : undefined}
				autoComplete="off"
				spellCheck={false}
				className="mt-1.5 h-9 w-full rounded-md border border-input bg-card px-2.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive"
			/>
			{problem && (
				<p id={problemId} role="alert" className="mt-2 text-sm text-destructive">
					{problem}
				</p>
			)}
		</Dialog>
	);
}
