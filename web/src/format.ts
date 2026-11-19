// How sizes and dates read in the list. English for now; Hungarian comes with its own ticket.

const SIZE_UNITS = ["KB", "MB", "GB", "TB"];

/** A file size the way storage is sold: 1 KB is 1000 bytes. "84 KB", "1.2 MB". */
export function formatSize(bytes: number): string {
	if (bytes < 1000) return `${bytes} B`;
	let value = bytes / 1000;
	let unit = 0;
	while (unit < SIZE_UNITS.length - 1 && Math.round(value) >= 1000) {
		value /= 1000;
		unit++;
	}
	const shown = value < 9.95 ? value.toFixed(1) : String(Math.round(value));
	return `${shown} ${SIZE_UNITS[unit]}`;
}

const DAY = 24 * 60 * 60 * 1000;
const sameYear = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short" });
const otherYear = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short", year: "numeric" });
const full = new Intl.DateTimeFormat("en-GB", { dateStyle: "long", timeStyle: "short" });

/** When something changed, in the list's short form: "Today", "Yesterday", "12 Nov", "12 Nov 2025". */
export function formatChanged(iso: string, now = new Date()): string {
	const date = new Date(iso);
	const midnight = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
	const daysAgo = Math.round((midnight(now) - midnight(date)) / DAY);
	if (daysAgo === 0) return "Today";
	if (daysAgo === 1) return "Yesterday";
	return (date.getFullYear() === now.getFullYear() ? sameYear : otherYear).format(date);
}

/** The full date and time, for a tooltip. */
export function formatChangedFull(iso: string): string {
	return full.format(new Date(iso));
}
