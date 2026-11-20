// Whether the page is on a narrow screen (a phone): below Tailwind's md, 48rem (768 px). Layout follows it with md: and
// max-md: classes; what a phone does differently (a sheet for the details, ticking only after "Select" or a press and
// hold, the bulk bar at the bottom) follows useNarrow.
import { useSyncExternalStore } from "react";

const NARROW = "(max-width: 47.99rem)";

function subscribe(changed: () => void) {
	const m = window.matchMedia(NARROW);
	m.addEventListener("change", changed);
	return () => m.removeEventListener("change", changed);
}

/** True while the page is narrower than 48rem. */
export function useNarrow(): boolean {
	return useSyncExternalStore(subscribe, () => window.matchMedia(NARROW).matches, () => false);
}

/**
 * Whether this browser lets a person choose a whole folder to upload. Phone browsers know the attribute
 * (webkitdirectory) but their pickers choose files only, so on them it is not offered.
 */
export const foldersChoosable = (() => {
	if (typeof document === "undefined" || !("webkitdirectory" in document.createElement("input"))) return false;
	const ua = navigator.userAgent;
	const phoneOrTablet = /Android|iPhone|iPad|iPod|Mobile/i.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
	return !phoneOrTablet;
})();

/** Whether the screen is touched rather than pointed at with a mouse: nothing is dragged onto it. */
export const touchOnly = () => typeof window !== "undefined" && window.matchMedia("(hover: none) and (pointer: coarse)").matches;
