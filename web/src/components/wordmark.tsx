/**
 * The Superit wordmark, as in neonjelly-co/superit. The dot of the i is the brand's status light: it is drawn, not
 * typed, so it stays green whatever colour the letters are. Size it with a text size class.
 */
export function Wordmark({ className = "" }: { className?: string }) {
	return (
		<span className={`inline-flex items-baseline font-bold leading-none tracking-[-0.02em] ${className}`}>
			<span className="sr-only">Superit</span>
			<span aria-hidden className="inline-flex items-baseline">
				super
				<span className="relative">
					ı
					{/* Placed over the font's own tittle: 0.14em below the content box top, which is the ascender line. */}
					<span className="absolute top-[0.14em] left-1/2 size-[0.24em] -translate-x-1/2 rounded-full bg-signal shadow-[0_0_0_0.07em_color-mix(in_oklab,var(--color-signal)_22%,transparent)]" />
				</span>
				t
			</span>
		</span>
	);
}
