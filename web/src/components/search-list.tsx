import { RotateCw } from "lucide-react";
import type { ReactNode } from "react";
import type { Found } from "../api";
import { formatChanged, formatChangedFull, formatSize } from "../format";
import { Button } from "./button";
import { FileIcon } from "./file-icon";
import { FileLink, FolderButton } from "./file-link";

type Props = {
	items?: Found[];
	loading: boolean;
	error?: Error | null;
	onRetry: () => void;
	/** Opens a folder: clicking its name. */
	onOpen: (found: Found) => void;
	/** Goes to the folder it is in: clicking where it is. */
	onGoTo: (found: Found) => void;
	/** The result whose details are shown, as "place:id", if any. */
	selected?: string;
	onSelect: (found: Found) => void;
};

/** A result is named by its place and its id: ids are the Drive's, across every place. */
export const keyOfFound = (f: Found) => `${f.place}:${f.id}`;

/**
 * Where a result is, as the person may see it: from "My files" or the shared folder's name when they can open the
 * place, and from "Shared with me" when it was shared with them in part of a place; then the folders above it that
 * they can open.
 */
export function locationOf(f: Found): string {
	const root = !f.fromTop ? "Shared with me" : f.placeKind === "my-files" ? "My files" : (f.placeName ?? "");
	return [root, ...f.path.map((p) => p.name)].join(" › ");
}

/**
 * What a search found: one row per file or folder with its name, where it is, when it changed and its size, the best
 * matches first. The click rule is the list's: a name opens (a folder in the list, a file in the browser or as a
 * download), where it is goes to the folder it is in, and the rest of the row selects it and shows its details.
 */
export function SearchList({ items, loading, error, onRetry, onOpen, onGoTo, selected, onSelect }: Props) {
	return (
		<div className="mt-5 overflow-hidden rounded-xl border border-border bg-card">
			<table className="w-full table-fixed text-sm">
				<thead>
					<tr className="h-11 border-b border-border text-left text-muted-foreground">
						<th scope="col" className="px-4 font-normal">
							Name
						</th>
						<th scope="col" className="w-64 px-4 font-normal">
							Where
						</th>
						<th scope="col" className="w-32 px-4 font-normal">
							Changed
						</th>
						<th scope="col" className="w-28 px-4 text-right font-normal">
							Size
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
						<Message>Searching…</Message>
					) : items.length === 0 ? (
						<Message>Nothing you can see has this in its name. Search for a shorter part of it, or check the spelling.</Message>
					) : (
						items.map((f) => {
							const key = keyOfFound(f);
							const where = locationOf(f);
							return (
								<tr
									key={key}
									tabIndex={0}
									aria-selected={key === selected}
									onClick={() => onSelect(f)}
									onKeyDown={(e) => {
										if (e.target === e.currentTarget && (e.key === "Enter" || e.key === " ")) {
											e.preventDefault();
											onSelect(f);
										}
									}}
									className={`cursor-default border-b border-border outline-none last:border-0 focus-visible:bg-muted ${key === selected ? "bg-muted" : "hover:bg-muted/50"}`}
								>
									<td className="px-4 py-2.5">
										<span className="flex min-w-0 items-center gap-2.5">
											<FileIcon item={f} />
											{f.kind === "file" ? <FileLink placeId={f.place} item={f} /> : <FolderButton item={f} onOpen={() => onOpen(f)} />}
										</span>
									</td>
									<td className="px-4 py-2.5">
										<button
											type="button"
											title={`Go to ${where}`}
											onClick={(e) => {
												e.stopPropagation();
												onGoTo(f);
											}}
											className="block max-w-full truncate text-left text-muted-foreground underline-offset-4 hover:text-foreground hover:underline focus-visible:underline"
										>
											{where}
										</button>
									</td>
									<td className="px-4 py-2.5 text-muted-foreground">
										{f.changed && (
											<time dateTime={f.changed} title={formatChangedFull(f.changed)}>
												{formatChanged(f.changed)}
											</time>
										)}
									</td>
									<td className="px-4 py-2.5 text-right text-muted-foreground tabular-nums">
										{f.size !== undefined ? formatSize(f.size) : ""}
									</td>
								</tr>
							);
						})
					)}
				</tbody>
			</table>
		</div>
	);
}

function Message({ children }: { children: ReactNode }) {
	return (
		<tr>
			<td colSpan={4} className="px-4 py-10 text-center text-muted-foreground">
				<div className="flex flex-col items-center">{children}</div>
			</td>
		</tr>
	);
}
