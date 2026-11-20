// How sizes and dates read, in the page's language (i18n): "1.2 MB" and "12 Nov" in English, "1,2 MB" and "nov. 12." in
// Hungarian. The formats are made on first use, once the page's language is known, and kept.
import { locale, t } from "./i18n";

const SIZE_UNITS = ["KB", "MB", "GB", "TB"];

let formats:
	| {
			oneDecimal: Intl.NumberFormat;
			sameYear: Intl.DateTimeFormat;
			otherYear: Intl.DateTimeFormat;
			full: Intl.DateTimeFormat;
			day: Intl.DateTimeFormat;
			moment: Intl.DateTimeFormat;
			dayMonth: Intl.DateTimeFormat;
			dayMonthYear: Intl.DateTimeFormat;
			time: Intl.DateTimeFormat;
	  }
	| undefined;

function f() {
	if (!formats) {
		const l = locale();
		formats = {
			oneDecimal: new Intl.NumberFormat(l, { minimumFractionDigits: 1, maximumFractionDigits: 1 }),
			sameYear: new Intl.DateTimeFormat(l, { day: "numeric", month: "short" }),
			otherYear: new Intl.DateTimeFormat(l, { day: "numeric", month: "short", year: "numeric" }),
			full: new Intl.DateTimeFormat(l, { dateStyle: "long", timeStyle: "short" }),
			day: new Intl.DateTimeFormat(l, { dateStyle: "medium" }),
			moment: new Intl.DateTimeFormat(l, { dateStyle: "medium", timeStyle: "short" }),
			dayMonth: new Intl.DateTimeFormat(l, { day: "numeric", month: "long" }),
			dayMonthYear: new Intl.DateTimeFormat(l, { day: "numeric", month: "long", year: "numeric" }),
			time: new Intl.DateTimeFormat(l, { hour: "2-digit", minute: "2-digit" }),
		};
	}
	return formats;
}

/**
 * A file size the way storage is sold: 1 KB is 1000 bytes. "84 KB", "1.2 MB" (Hungarian "1,2 MB"). The number and its
 * unit are joined by a no-break space, so a line never ends between them.
 */
export function formatSize(bytes: number): string {
	if (bytes < 1000) return `${bytes}\u00a0B`;
	let value = bytes / 1000;
	let unit = 0;
	while (unit < SIZE_UNITS.length - 1 && Math.round(value) >= 1000) {
		value /= 1000;
		unit++;
	}
	const shown = value < 9.95 ? f().oneDecimal.format(value) : String(Math.round(value));
	return `${shown}\u00a0${SIZE_UNITS[unit]}`;
}

/**
 * A storage's size, rounded the way it is sold: "1 TB", "500 GB", "1.5 TB". The Drive's own figures are "about"
 * anyway, so a capacity given in TiB reads as its nearest round size.
 */
export function formatCapacity(bytes: number): string {
	return formatSize(bytes).replace(/[.,]0(?=\s)/, "");
}

const DAY = 24 * 60 * 60 * 1000;

const midnight = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

/** When something changed, in the list's short form: "Today", "Yesterday", "12 Nov", "12 Nov 2025". */
export function formatChanged(iso: string, now = new Date()): string {
	const date = new Date(iso);
	const daysAgo = Math.round((midnight(now) - midnight(date)) / DAY);
	if (daysAgo === 0) return t.time.today;
	if (daysAgo === 1) return t.time.yesterday;
	return (date.getFullYear() === now.getFullYear() ? f().sameYear : f().otherYear).format(date);
}

/** How long until a day to come: "today", "tomorrow", "in 29 days". */
export function formatUntil(iso: string, now = new Date()): string {
	const days = Math.round((midnight(new Date(iso)) - midnight(now)) / DAY);
	if (days <= 0) return t.time.todayLower;
	if (days === 1) return t.time.tomorrow;
	return t.time.inDays(days);
}

/** The full date and time, for a tooltip or the details. */
export function formatChangedFull(iso: string): string {
	return f().full.format(new Date(iso));
}

/** A day, as a line of "who can see this" says until when: "20 Nov 2026". */
export function formatDay(iso: string): string {
	return f().day.format(new Date(iso));
}

/** A day and time, short: "20 Nov 2026, 09:41". */
export function formatMoment(iso: string): string {
	return f().moment.format(new Date(iso));
}

/** "18 November", with the year when it is not this one. */
export function formatDayMonth(iso: string, now = new Date()): string {
	const d = new Date(iso);
	return (d.getFullYear() === now.getFullYear() ? f().dayMonth : f().dayMonthYear).format(d);
}

/** The time of day: "04:00". */
export function formatTime(iso: string): string {
	return f().time.format(new Date(iso));
}
