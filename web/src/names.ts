// What the Drive says about a name before sending anything, so a person is not made to wait for a whole file to
// travel only to be refused. The Drive checks again and its answer is the one that counts (internal/drive/files.go);
// the messages here (t.names) are the same as its own, in both languages.
import { t } from "./i18n";

/** The longest name the Drive stores, in bytes of UTF-8. */
export const MAX_NAME_BYTES = 255;

const encoder = new TextEncoder();

/**
 * A name as the Drive is sent it: in Unicode's composed form (NFC). Safari on macOS gives decomposed names (an "é"
 * as "e" and an accent), which would otherwise sit beside the same name typed in another browser, looking the same.
 */
export const composed = (name: string) => name.normalize("NFC");

/** Why a file or folder cannot have this name, or undefined when it can. */
export function nameProblem(name: string): string | undefined {
	if (name === "") return t.names.needed;
	if (encoder.encode(name).length > MAX_NAME_BYTES) return t.names.tooLong;
	if (name === "." || name === "..") return t.names.dots;
	if (name.includes("/")) return t.names.slash;
	return undefined;
}

/**
 * The first name of "Name (2)", "Name (3)" and so on, before a file's extension, that none of the items has; the
 * Drive names a kept-both upload the same way. Used to suggest a name for a new folder.
 */
export function freeName(name: string, items: { name: string }[] | undefined, folder: boolean): string {
	const taken = new Set(items?.map((i) => i.name));
	if (!taken.has(name)) return name;
	const dot = folder ? -1 : name.lastIndexOf(".");
	const [stem, ext] = dot > 0 ? [name.slice(0, dot), name.slice(dot)] : [name, ""];
	for (let n = 2; n < 1000; n++) {
		const candidate = `${stem} (${n})${ext}`;
		if (!taken.has(candidate)) return candidate;
	}
	return name;
}
