// The interface's language: English or Hungarian, chosen from the browser's languages, or by the person with the switch
// (kept in this browser). Each page loads the words of its one language before it shows anything (start), so a page is
// never in two languages, and only that language's words are downloaded. Switching loads the page again in the other
// language, the Drive's own answers with it: every call says the language (Accept-Language, in api.ts).
import { Fragment, type ReactNode, createElement } from "react";
import type { Messages } from "./en";

export type { Messages } from "./en";
export type Lang = "en" | "hu";

/** Where this browser keeps the language the person chose. */
const CHOSEN = "superit-drive.language";

/** The words of the page's language. Set by start, before the first render; read anywhere after it. */
export let t: Messages;
/** The page's language. */
export let lang: Lang = "en";

/** The language the person chose in this browser, if they did. */
function chosen(): Lang | undefined {
	try {
		const v = window.localStorage.getItem(CHOSEN);
		return v === "en" || v === "hu" ? v : undefined;
	} catch {
		return undefined;
	}
}

/**
 * The language of the page: the one chosen in this browser; otherwise the first of the browser's languages that the Drive
 * speaks (as the Drive itself picks for a browser's Accept-Language); English when it names neither.
 */
export function pick(chosenHere: Lang | undefined, browser: readonly string[]): Lang {
	if (chosenHere) return chosenHere;
	for (const tag of browser) {
		const primary = tag.toLowerCase().split("-")[0];
		if (primary === "hu" || primary === "en") return primary;
	}
	return "en";
}

/** Loads the words of the page's language. Call once, and render only when it is done. */
export async function start(): Promise<void> {
	lang = pick(chosen(), navigator.languages ?? [navigator.language]);
	t = lang === "hu" ? (await import("./hu")).hu : (await import("./en")).en;
	document.documentElement.lang = lang;
}

/** Switches to the other language: kept in this browser, and the page loaded again in it. */
export function switchLanguage() {
	try {
		window.localStorage.setItem(CHOSEN, t.language.otherCode);
	} catch {
		// Without storage the switch cannot be kept: the page stays as it is.
		return;
	}
	window.location.reload();
}

/** The locale dates and numbers are written in: British English, or Hungarian. */
export const locale = () => (lang === "hu" ? "hu-HU" : "en-GB");

/**
 * A text with pieces of the page in it: each {name} in the text becomes the piece of that name (an address in the mono
 * font, say), wherever the language puts it.
 */
export function fill(text: string, pieces: Record<string, ReactNode>): ReactNode {
	const parts = text.split(/\{(\w+)\}/);
	return createElement(
		Fragment,
		null,
		...parts.map((part, i) => (i % 2 === 1 ? createElement(Fragment, { key: i }, pieces[part] ?? `{${part}}`) : part)),
	);
}
