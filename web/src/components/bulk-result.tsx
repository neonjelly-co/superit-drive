import { Button } from "./button";
import { Dialog } from "./dialog";

/** One item a change asked of several did not happen to, and why, in the Drive's words. */
export type Failure = { name: string; message: string };

/** What a change asked of several items at once came to: what it was done to, and what it was not. */
export type BulkOutcome<T> = { done: T[]; failed: Failure[] };

/**
 * Does a change to several items, one after another, and says what happened to each: a change that fails for one
 * item (its name is taken where it goes, someone moved it meanwhile) does not stop the others.
 */
export async function eachOf<I extends { name: string }, T>(
	items: I[],
	change: (item: I) => Promise<T>,
	progress?: (finished: number) => void,
): Promise<BulkOutcome<T>> {
	const out: BulkOutcome<T> = { done: [], failed: [] };
	for (const [i, item] of items.entries()) {
		try {
			out.done.push(await change(item));
		} catch (e) {
			out.failed.push({ name: item.name, message: e instanceof Error ? e.message : String(e) });
		}
		progress?.(i + 1);
	}
	return out;
}

type Props = {
	/** What was not done, as a title: "2 of 5 items were not moved". */
	title: string;
	/** What was done, in one sentence: "3 were moved to Payroll." */
	done: string;
	/** What happened to the others: "They are still where they were." */
	stayed: string;
	failed: Failure[];
	/** Undoing what was done, when it can be. */
	undo?: { label: string; run: () => void };
	onClose: () => void;
};

/**
 * Says plainly what happened when a change asked of several items at once did not happen to all of them: how many it
 * was done to, and each item it was not done to, with the Drive's reason.
 */
export function BulkResultDialog({ title, done, stayed, failed, undo, onClose }: Props) {
	return (
		<Dialog
			wide
			title={title}
			description={`${done} ${stayed}`}
			onClose={onClose}
			onSubmit={onClose}
			footer={
				<>
					{undo && (
						<Button
							onClick={() => {
								undo.run();
								onClose();
							}}
						>
							{undo.label}
						</Button>
					)}
					<Button variant="primary" type="submit">
						Close
					</Button>
				</>
			}
		>
			<ul aria-label="Not done" className="max-h-72 overflow-auto rounded-lg border border-border px-3 text-sm">
				{failed.map((f, i) => (
					// The same name can fail twice (two files of one name from different folders): the place in the list is the key.
					<li key={i} className="border-b border-border py-2 last:border-0">
						<p className="font-medium break-words">{f.name}</p>
						<p className="mt-0.5 text-destructive">{f.message}</p>
					</li>
				))}
			</ul>
		</Dialog>
	);
}
