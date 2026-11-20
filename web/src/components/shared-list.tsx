import { RotateCw } from "lucide-react";
import type { ReactNode } from "react";
import { nameOf } from "../access";
import type { SharedItem } from "../api";
import { formatChanged, formatChangedFull, formatSize } from "../format";
import { t } from "../i18n";
import { useNarrow } from "../narrow";
import { Button } from "./button";
import { FileIcon } from "./file-icon";
import { FileLink, FolderButton } from "./file-link";
import { PhoneList, PhoneMessage, PhoneRow } from "./phone-row";

type Props = {
	items?: SharedItem[];
	loading: boolean;
	error?: Error | null;
	onRetry: () => void;
	/** Opens a folder: clicking its name. */
	onOpen: (item: SharedItem) => void;
	/** The item whose details are shown, as "place:id", if any. */
	selected?: string;
	onSelect: (item: SharedItem) => void;
};

/** An item of "Shared with me" is named by its place and its id: ids are the Drive's, across every place. */
export const keyOf = (item: SharedItem) => `${item.place}:${item.id}`;

/** Who shared it, for the list: their name, and whether with everyone in the project. */
export function sharedByOf(item: SharedItem): string {
	const by = item.sharedBy ? nameOf(item.sharedBy) : t.common.someoneWhoLeft;
	return item.toEveryone ? t.shared.withEveryone(by) : by;
}

/**
 * The "Shared with me" view: what others shared with the person that they reach only through that share, from someone's
 * My files or from a shared folder they have no role on, the most recently shared first. Each is the highest point of
 * what was shared: what is inside a shared folder is found by opening it. The click rule is the list's: a name opens
 * (a folder in the list, a file in the browser or as a download), the rest of the row selects it and shows its details.
 * On a phone, a row is the name with who shared it and its size under it, and ⋯ for its details.
 */
export function SharedList({ items, loading, error, onRetry, onOpen, selected, onSelect }: Props) {
	const narrow = useNarrow();
	const message = error ? (
		<>
			<span className="text-destructive">{error.message}</span>
			<Button className="mt-3" onClick={onRetry}>
				<RotateCw aria-hidden /> {t.common.tryAgain}
			</Button>
		</>
	) : loading || !items ? (
		t.common.loading
	) : items.length === 0 ? (
		t.shared.empty
	) : undefined;
	if (narrow) {
		if (message !== undefined || !items) return <PhoneMessage>{message}</PhoneMessage>;
		return (
			<PhoneList>
				{items.map((item) => (
					<PhoneRow
						key={keyOf(item)}
						item={item}
						line={[sharedByOf(item), item.size !== undefined ? formatSize(item.size) : ""].filter(Boolean).join(" · ")}
						open={item.kind === "file" ? { placeId: item.place, file: item } : { folder: () => onOpen(item) }}
						onDetails={() => onSelect(item)}
						detailsLabel={t.list.details(item.name)}
						ticked={keyOf(item) === selected}
					/>
				))}
			</PhoneList>
		);
	}
	return (
		<div className="mt-5 overflow-hidden rounded-xl border border-border bg-card">
			<table className="w-full table-fixed text-sm">
				<thead>
					<tr className="h-11 border-b border-border text-left text-muted-foreground">
						<th scope="col" className="px-4 font-normal">
							{t.common.name}
						</th>
						<th scope="col" className="w-56 px-4 font-normal">
							{t.shared.sharedBy}
						</th>
						<th scope="col" className="w-32 px-4 font-normal">
							{t.shared.shared}
						</th>
						<th scope="col" className="w-28 px-4 text-right font-normal">
							{t.common.size}
						</th>
					</tr>
				</thead>
				<tbody>
					{message !== undefined || !items ? (
						<Message>{message}</Message>
					) : (
						items.map((item) => {
							const key = keyOf(item);
							const by = sharedByOf(item);
							return (
								<tr
									key={key}
									tabIndex={0}
									aria-selected={key === selected}
									onClick={() => onSelect(item)}
									onKeyDown={(e) => {
										if (e.target === e.currentTarget && (e.key === "Enter" || e.key === " ")) {
											e.preventDefault();
											onSelect(item);
										}
									}}
									className={`cursor-default border-b border-border outline-none last:border-0 focus-visible:bg-muted ${key === selected ? "bg-muted" : "hover:bg-muted/50"}`}
								>
									<td className="px-4 py-2.5">
										<span className="flex min-w-0 items-center gap-2.5">
											<FileIcon item={item} />
											{item.kind === "file" ? (
												<FileLink placeId={item.place} item={item} />
											) : (
												<FolderButton item={item} onOpen={() => onOpen(item)} />
											)}
										</span>
									</td>
									<td className="truncate px-4 py-2.5" title={item.sharedBy?.email ?? by}>
										{by}
									</td>
									<td className="px-4 py-2.5 text-muted-foreground">
										<time dateTime={item.shared} title={formatChangedFull(item.shared)}>
											{formatChanged(item.shared)}
										</time>
									</td>
									<td className="px-4 py-2.5 text-right text-muted-foreground tabular-nums">
										{item.size !== undefined ? formatSize(item.size) : ""}
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
