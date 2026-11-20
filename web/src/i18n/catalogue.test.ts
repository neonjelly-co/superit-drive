// The interface's words: every text is in English and Hungarian, the Hungarian translated (not the English again), and
// no component says anything in English of its own around the catalogue. Run with `bun test` (make test runs it).
import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { parseAst } from "vite";
import { en } from "./en";
import { hu } from "./hu";
import { pick } from "./index";

type Entry = string | ((...a: never[]) => string);
type Catalogue = Record<string, Record<string, Entry>>;

/** Texts that are the same in both languages on purpose: names, and a value passed through as it is. */
const SAME = new Set(["app.drive", "app.driveOf", "who.one", "rollback.at"]);

const sample = (n: number) => Array.from({ length: n }, (_, i) => (i % 2 === 0 ? `Érték${i}` : i + 3)) as never[];
const say = (e: Entry) => (typeof e === "function" ? e(...sample(e.length)) : e);
const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();

describe("the catalogues", () => {
	const entries = (c: Catalogue) =>
		Object.entries(c).flatMap(([group, words]) => Object.entries(words).map(([key, e]) => [`${group}.${key}`, e] as const));
	const english = new Map(entries(en as unknown as Catalogue));
	const hungarian = new Map(entries(hu as unknown as Catalogue));

	test("have the same keys", () => {
		expect([...hungarian.keys()].sort()).toEqual([...english.keys()].sort());
	});

	test("say every text in Hungarian, translated, with the same values and pieces", () => {
		const problems: string[] = [];
		for (const [key, e] of english) {
			const h = hungarian.get(key);
			if (h === undefined) continue;
			if (typeof e !== typeof h || (typeof e === "function" && typeof h === "function" && e.length !== h.length)) {
				problems.push(`${key}: the Hungarian takes other values than the English`);
				continue;
			}
			const [a, b] = [say(e), say(h)];
			if (!b.trim()) problems.push(`${key}: no Hungarian`);
			else if (a === b && !SAME.has(key)) problems.push(`${key}: "${a}" is the same in Hungarian, so left untranslated`);
			if (placeholders(a).join() !== placeholders(b).join()) problems.push(`${key}: the pieces {…} differ: "${a}" / "${b}"`);
			// Every value given is said: a number or a name dropped would lose what the person needs to know.
			if (typeof h === "function") {
				for (const v of sample(h.length) as unknown as (string | number)[]) {
					if (!b.includes(String(v)) && key !== "who.guests" && key !== "who.onlyGuests" && key !== "conflict.forOthers") {
						problems.push(`${key}: the Hungarian does not say the value ${v}: "${b}"`);
					}
				}
			}
		}
		expect(problems).toEqual([]);
	});

	test("never put a suffix or an article on a value", () => {
		// "a(z)" and a hyphen after a value ("3-at", "„Számla”-t") are what a value's sounds would have to decide.
		const problems: string[] = [];
		for (const [key, h] of hungarian) {
			const b = say(h);
			if (/a\(z\)/i.test(b) || /(Érték\d+|\d)[”"]?-\p{L}/u.test(b) || /”-?\p{Ll}/u.test(b)) problems.push(`${key}: "${b}"`);
		}
		expect(problems).toEqual([]);
	});
});

describe("the language of a page", () => {
	test("is the one chosen in this browser, else the first of the browser's that the Drive speaks", () => {
		expect(pick("en", ["hu-HU", "hu"])).toBe("en");
		expect(pick(undefined, ["hu-HU", "hu", "en-US"])).toBe("hu");
		expect(pick(undefined, ["de-DE", "hu", "en"])).toBe("hu");
		expect(pick(undefined, ["en-GB", "hu"])).toBe("en");
		expect(pick(undefined, ["de-DE", "fr"])).toBe("en");
		expect(pick(undefined, [])).toBe("en");
	});
});

/** Every source file of the interface but the catalogues and the tests. */
function sources(dir: string): string[] {
	return readdirSync(dir).flatMap((name) => {
		const path = join(dir, name);
		if (statSync(path).isDirectory()) return name === "i18n" ? [] : sources(path);
		return /\.tsx?$/.test(name) && !name.endsWith(".test.ts") ? [path] : [];
	});
}

/** Attributes a person reads (or hears, from a screen reader). */
const READ = new Set(["title", "aria-label", "placeholder", "alt", "label", "aria-valuetext", "description", "submitLabel"]);
/** Words that are names, the same in every language. */
const NAMES = new Set(["Superit", "super", "ı", "t", "Drive"]);
/** A string that reads as words for a person: a capital, then at least two more words. */
const SENTENCE = /^[“"„]?\p{Lu}[\p{Ll}']*,?( [\p{Ll}“„'’][^ ]*){2,}/u;

describe("the components", () => {
	const root = join(import.meta.dir, "..");
	for (const file of sources(root)) {
		test(`${relative(root, file)} says nothing a person reads outside the catalogue`, () => {
			const code = readFileSync(file, "utf8");
			const found: string[] = [];
			const visit = (node: unknown, parent?: { type?: string; name?: { name?: string } }) => {
				if (!node || typeof node !== "object") return;
				if (Array.isArray(node)) {
					for (const n of node) visit(n, parent);
					return;
				}
				const n = node as Record<string, unknown> & { type?: string };
				if (n.type === "JSXText") {
					const text = String(n.value).trim();
					if (/\p{L}/u.test(text) && !NAMES.has(text)) found.push(`text "${text}"`);
				}
				if (n.type === "JSXAttribute") {
					const name = (n.name as { name?: string })?.name ?? "";
					const value = n.value as { type?: string; value?: unknown } | null;
					if (READ.has(name) && value?.type === "Literal" && /\p{L}/u.test(String(value.value))) found.push(`${name}="${value.value}"`);
				}
				if (n.type === "Literal" && typeof n.value === "string" && SENTENCE.test(n.value)) found.push(`"${n.value}"`);
				if (n.type === "TemplateElement") {
					const raw = String((n.value as { raw?: string })?.raw ?? "");
					if (SENTENCE.test(raw.trim())) found.push(`\`${raw}\``);
				}
				for (const [k, v] of Object.entries(n)) if (k !== "type") visit(v, n as never);
			};
			visit(parseAst(code, { lang: file.endsWith(".tsx") ? "tsx" : "ts" }));
			expect(found).toEqual([]);
		});
	}
});
