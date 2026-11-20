import { useId, useState } from "react";
import { t } from "../i18n";
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
			title={folder ? t.conflict.folderThere(question.name) : t.conflict.fileThere(question.name)}
			description={folder ? t.conflict.folderNote : t.conflict.fileNote}
			onClose={() => choose("skip")}
			footer={
				<>
					<Button variant="ghost" onClick={() => choose("skip")}>
						{t.conflict.skip}
					</Button>
					<Button variant={folder ? "primary" : "outline"} onClick={() => choose("keep-both")}>
						{t.conflict.keepBoth}
					</Button>
					{!folder && (
						<Button variant="primary" onClick={() => choose("replace")}>
							{t.conflict.replace}
						</Button>
					)}
				</>
			}
		>
			{question.others > 0 && (
				<label htmlFor={checkboxId} className="flex items-center gap-2 text-sm max-md:min-h-11">
					<input
						id={checkboxId}
						type="checkbox"
						checked={forOthers}
						onChange={(e) => setForOthers(e.target.checked)}
						className="size-4 accent-primary"
					/>
					{t.conflict.forOthers(question.others)}
				</label>
			)}
		</Dialog>
	);
}
