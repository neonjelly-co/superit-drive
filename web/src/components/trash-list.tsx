import { RotateCcw, RotateCw } from "lucide-react";
import type { ReactNode } from "react";
import { formatChanged, formatChangedFull, formatSize, formatUntil } from "../format";
import { Button } from "./button";
import { FileIcon } from "./file-icon";

/** One thing in the trash: an item of a place, or a whole shared folder. */
export type TrashRow = {
	key: string;
	name: string;
	kind: "file" | "folder" | "shared-folder";
	/** A file's size, or what the files in a folder add up to. */
	size: number;
	/** When it went to the trash, and when the Drive removes it for good (ISO 8601). */
	deleted: string;
	removed: string;
	/** True when an upload replaced it, rather than a person deleting it. */
	replaced?: boolean;
	/** Where it goes back to when restored, such as "Clients › Bakery Ltd". */
	backTo: string;
};

type Props = {
	rows?: TrashRow[];
	loading: boolean;
	error?: Error | null;
	onRetry: () => void;
	onRestore: (row: TrashRow) => void;
	/** The key of the row being restored, if any. */
	restoring?: string;
};

/**
 * The Trash view: what was deleted (or replaced by an upload) in "My files" and in the shared folders the person may
 * restore from, and shared folders deleted whole; when, where each goes back to, and when the Drive removes it for
 * good. Restore puts it back where it was.
 */
export function TrashList({ rows, loading, error, onRetry, onRestore, restoring }: Props) {
	return (
		<div className="mt-5 overflow-hidden rounded-xl border border-border bg-card">
			<table className="w-full table-fixed text-sm">
				<thead>
					<tr className="h-11 border-b border-border text-left text-muted-foreground">
						<th scope="col" className="px-4 font-normal">
							Name
						</th>
						<th scope="col" className="w-48 px-4 font-normal">
							Goes back to
						</th>
						<th scope="col" className="w-28 px-4 font-normal">
							Deleted
						</th>
						<th scope="col" className="w-40 px-4 font-normal whitespace-nowrap">
							Removed for good
						</th>
						<th scope="col" className="w-24 px-4 text-right font-normal">
							Size
						</th>
						<th scope="col" className="w-32 px-4">
							<span className="sr-only">Restore</span>
						</th>
					</tr>
				</thead>
				<tbody>
					{error ? (
						<Message>
							<span className="text-destructive">{error.message}</span>
							<Button className="mt-3" onClick={onRetry}>
								<RotateCw aria-hidden /> Try again
							</Button>
						</Message>
					) : loading || !rows ? (
						<Message>Loading…</Message>
					) : rows.length === 0 ? (
						<Message>The trash is empty.</Message>
					) : (
						rows.map((row) => (
							<tr key={row.key} className="border-b border-border last:border-0">
								<td className="px-4 py-2.5">
									<span className="flex min-w-0 items-center gap-2.5">
										<FileIcon item={row} />
										<span className="min-w-0">
											<span className="block truncate font-medium" title={row.name}>
												{row.name}
											</span>
											{row.replaced && <span className="block text-muted-foreground">Replaced by an upload</span>}
											{row.kind === "shared-folder" && (
												<span className="block text-muted-foreground">Shared folder, with everything in it</span>
											)}
										</span>
									</span>
								</td>
								<td className="truncate px-4 py-2.5 text-muted-foreground" title={row.backTo}>
									{row.backTo}
								</td>
								<td className="px-4 py-2.5 text-muted-foreground">
									<time dateTime={row.deleted} title={formatChangedFull(row.deleted)}>
										{formatChanged(row.deleted)}
									</time>
								</td>
								<td className="px-4 py-2.5 text-muted-foreground">
									<time dateTime={row.removed} title={formatChangedFull(row.removed)}>
										{formatUntil(row.removed)}
									</time>
								</td>
								<td className="px-4 py-2.5 text-right text-muted-foreground tabular-nums">{formatSize(row.size)}</td>
								<td className="px-4 py-1.5 text-right">
									<Button small onClick={() => onRestore(row)} disabled={restoring !== undefined} aria-label={`Restore ${row.name}`}>
										<RotateCcw aria-hidden /> Restore
									</Button>
								</td>
							</tr>
						))
					)}
				</tbody>
			</table>
		</div>
	);
}

function Message({ children }: { children: ReactNode }) {
	return (
		<tr>
			<td colSpan={6} className="px-4 py-10 text-center text-muted-foreground">
				<div className="flex flex-col items-center">{children}</div>
			</td>
		</tr>
	);
}
