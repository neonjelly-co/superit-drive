import { RotateCcw, RotateCw } from "lucide-react";
import type { ReactNode } from "react";
import type { TrashItem } from "../api";
import { formatChanged, formatChangedFull, formatSize, formatUntil } from "../format";
import { Button } from "./button";
import { FileIcon } from "./file-icon";

type Props = {
	items?: TrashItem[];
	loading: boolean;
	error?: Error | null;
	onRetry: () => void;
	onRestore: (item: TrashItem) => void;
	/** The item being restored, if any. */
	restoring?: number;
};

/**
 * The Trash view: what was deleted (or replaced by an upload), when, where it goes back to, and when the Drive removes
 * it for good. Restore puts it back where it was.
 */
export function TrashList({ items, loading, error, onRetry, onRestore, restoring }: Props) {
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
					) : loading || !items ? (
						<Message>Loading…</Message>
					) : items.length === 0 ? (
						<Message>The trash is empty.</Message>
					) : (
						items.map((item) => (
							<tr key={item.id} className="border-b border-border last:border-0">
								<td className="px-4 py-2.5">
									<span className="flex min-w-0 items-center gap-2.5">
										<FileIcon item={item} />
										<span className="min-w-0">
											<span className="block truncate font-medium" title={item.name}>
												{item.name}
											</span>
											{item.replaced && <span className="block text-muted-foreground">Replaced by an upload</span>}
										</span>
									</span>
								</td>
								<td className="truncate px-4 py-2.5 text-muted-foreground">
									{["My files", ...item.from.map((f) => f.name)].join(" › ")}
								</td>
								<td className="px-4 py-2.5 text-muted-foreground">
									<time dateTime={item.deleted} title={formatChangedFull(item.deleted)}>
										{formatChanged(item.deleted)}
									</time>
								</td>
								<td className="px-4 py-2.5 text-muted-foreground">
									<time dateTime={item.removed} title={formatChangedFull(item.removed)}>
										{formatUntil(item.removed)}
									</time>
								</td>
								<td className="px-4 py-2.5 text-right text-muted-foreground tabular-nums">{formatSize(item.size)}</td>
								<td className="px-4 py-1.5 text-right">
									<Button small onClick={() => onRestore(item)} disabled={restoring !== undefined} aria-label={`Restore ${item.name}`}>
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
