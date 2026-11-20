import { api, type Item } from "../api";
import { t } from "../i18n";

const nameLink = "truncate font-medium underline-offset-4 hover:underline focus-visible:underline";

/**
 * A file's name as a link, for the click rule "clicking a name opens it". A file of a kind the browser shows (the Drive
 * says which, `opens`) opens in a new tab, from the Drive's own address for opening; any other file downloads, from the
 * address that always sends it to be saved. The row it is in is not selected by the click.
 */
export function FileLink({ placeId, item }: { placeId: number; item: Item }) {
	if (item.opens) {
		return (
			<a
				href={api.openUrl(placeId, item.id)}
				target="_blank"
				rel="noopener noreferrer"
				title={t.list.openNamed(item.name)}
				onClick={(e) => e.stopPropagation()}
				className={nameLink}
			>
				{item.name}
			</a>
		);
	}
	return (
		<a href={api.downloadUrl(placeId, item.id)} download title={t.list.downloadNamed(item.name)} onClick={(e) => e.stopPropagation()} className={nameLink}>
			{item.name}
		</a>
	);
}

/** A folder's name as a button that opens it in the list. */
export function FolderButton({ item, onOpen }: { item: { name: string }; onOpen: () => void }) {
	return (
		<button
			type="button"
			title={t.list.openNamed(item.name)}
			onClick={(e) => {
				e.stopPropagation();
				onOpen();
			}}
			className={`${nameLink} text-left`}
		>
			{item.name}
		</button>
	);
}
