import { Languages } from "lucide-react";
import { switchLanguage, t } from "../i18n";

/** The switch to the other language, said in that language; the page loads again in it. */
export function LanguageSwitch({ className = "" }: { className?: string }) {
	return (
		<button
			type="button"
			onClick={switchLanguage}
			lang={t.language.otherCode}
			title={t.sidebar.language}
			className={`inline-flex min-h-9 items-center gap-2 rounded-md px-2.5 text-sm text-muted-foreground hover:bg-muted hover:text-foreground max-md:min-h-11 ${className}`}
		>
			<Languages aria-hidden className="size-4" /> {t.language.other}
		</button>
	);
}
