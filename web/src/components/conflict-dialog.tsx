import { useId, useState } from "react";
import type { Answer, Question } from "../uploads";
import { Button } from "./button";
import { Dialog } from "./dialog";

/**
 * "Replace or keep both", asked before a file whose name is already there is uploaded. Replacing puts the file that
 * is there into the trash; keeping both gives the new one a name with " (2)" or the like. A folder is never replaced.
 */
export function ConflictDialog({ question, onAnswer }: { question: Question; onAnswer: (a: Answer, forOthers: boolean) => void }) {
	const [forOthers, setForOthers] = useState(false);
	const checkboxId = useId();
	const folder = question.taken === "folder";
	const choose = (a: Answer) => onAnswer(a, forOthers);
	return (
		<Dialog
			title={folder ? `There is a folder called “${question.name}” here` : `“${question.name}” is already here`}
			description={
				folder
					? "A file cannot replace a folder. Keep both, and the file gets a number added to its name."
					: "Replace it with the one you are uploading, or keep both. A replaced file goes to the trash, where you can get it back for 30 days."
			}
			onClose={() => choose("skip")}
			footer={
				<>
					<Button variant="ghost" onClick={() => choose("skip")}>
						Skip this file
					</Button>
					<Button variant={folder ? "primary" : "outline"} onClick={() => choose("keep-both")}>
						Keep both
					</Button>
					{!folder && (
						<Button variant="primary" onClick={() => choose("replace")}>
							Replace
						</Button>
					)}
				</>
			}
		>
			{question.others > 0 && (
				<label htmlFor={checkboxId} className="flex items-center gap-2 text-sm">
					<input
						id={checkboxId}
						type="checkbox"
						checked={forOthers}
						onChange={(e) => setForOthers(e.target.checked)}
						className="size-4 accent-primary"
					/>
					Do the same for the {question.others === 1 ? "other file" : `other ${question.others} files`} whose names are
					already here
				</label>
			)}
		</Dialog>
	);
}
