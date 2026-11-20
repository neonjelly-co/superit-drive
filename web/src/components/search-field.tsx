import { Search, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { t } from "../i18n";

type Props = {
	/** The search the address holds; empty when not searching. */
	query: string;
	/** Searches for text: as it is typed (after a short pause), or at once with Enter. */
	onSearch: (text: string, typing: boolean) => void;
	/** Ends the search: the field emptied, or Escape. */
	onClear: () => void;
};

/**
 * The search field above the list: it finds files and folders by name, across everything the person can see. It
 * searches as the person types, after a short pause; "/" anywhere on the page puts the cursor in it.
 */
export function SearchField({ query, onSearch, onClear }: Props) {
	const [text, setText] = useState(query);
	const input = useRef<HTMLInputElement>(null);
	// The address can change the search (Back, Forward): the field follows it.
	useEffect(() => setText(query), [query]);
	useEffect(() => {
		if (text.trim() === query.trim()) return;
		const timer = setTimeout(() => (text.trim() ? onSearch(text, true) : onClear()), 300);
		return () => clearTimeout(timer);
	}, [text, query, onSearch, onClear]);
	useEffect(() => {
		const focus = (e: KeyboardEvent) => {
			const target = e.target as HTMLElement | null;
			if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return;
			if (target?.closest("input, textarea, select, [contenteditable=true], dialog")) return;
			e.preventDefault();
			input.current?.focus();
		};
		window.addEventListener("keydown", focus);
		return () => window.removeEventListener("keydown", focus);
	}, []);

	return (
		<form
			role="search"
			onSubmit={(e) => {
				e.preventDefault();
				if (text.trim()) onSearch(text, false);
			}}
			className="relative w-full max-w-xl"
		>
			<Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
			<input
				ref={input}
				type="search"
				value={text}
				maxLength={255}
				onChange={(e) => setText(e.currentTarget.value)}
				onKeyDown={(e) => {
					if (e.key === "Escape" && text) {
						e.preventDefault();
						setText("");
						onClear();
					}
				}}
				placeholder={t.search.placeholder}
				aria-label={t.search.label}
				aria-keyshortcuts="/"
				className="h-10 w-full rounded-lg border border-input bg-card pr-9 pl-9 text-sm outline-none placeholder:text-muted-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 max-md:h-11 max-md:pr-12 max-md:text-base [&::-webkit-search-cancel-button]:hidden"
			/>
			{text && (
				<button
					type="button"
					aria-label={t.search.clear}
					onClick={() => {
						setText("");
						onClear();
						input.current?.focus();
					}}
					className="absolute top-1/2 right-1.5 grid size-7 -translate-y-1/2 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground max-md:right-0 max-md:size-11"
				>
					<X aria-hidden className="size-4" />
				</button>
			)}
		</form>
	);
}
