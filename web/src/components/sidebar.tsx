import { Upload } from "lucide-react";
import { useRef } from "react";
import { Wordmark } from "./wordmark";

type Props = {
	project?: string;
	/** Called with the files the person chose; undefined while there is nowhere to upload to yet. */
	onUpload?: (files: File[]) => void;
};

/**
 * The left sidebar of layout D. In the full design it holds, from the top: Upload and New folder, My files, the shared
 * folders by name, New shared folder, Shared with me, Trash, and the storage meter at the bottom. Each arrives with the
 * ticket that makes it work; so far there are Upload and "My files".
 */
export function Sidebar({ project, onUpload }: Props) {
	const chooser = useRef<HTMLInputElement>(null);
	return (
		<aside className="flex w-60 shrink-0 flex-col border-r border-border bg-card p-3">
			<div className="px-2.5 py-2">
				<Wordmark className="text-xl" />
				<p className="mt-1 truncate text-sm text-muted-foreground" title={project}>
					Drive{project ? ` · ${project}` : ""}
				</p>
			</div>
			<div className="mt-3 grid gap-2">
				<button
					type="button"
					disabled={!onUpload}
					onClick={() => chooser.current?.click()}
					className="inline-flex h-9 items-center justify-start gap-1.5 rounded-md bg-primary px-2.5 text-sm font-medium text-primary-foreground outline-none hover:bg-primary/80 focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50"
				>
					<Upload aria-hidden className="size-4" /> Upload
				</button>
				<input
					ref={chooser}
					type="file"
					multiple
					hidden
					tabIndex={-1}
					onChange={(e) => {
						const files = Array.from(e.currentTarget.files ?? []);
						e.currentTarget.value = ""; // so choosing the same file again is noticed
						if (files.length > 0) onUpload?.(files);
					}}
				/>
			</div>
			<nav aria-label="Places" className="mt-4 space-y-0.5">
				<a
					href="/"
					aria-current="page"
					className="flex w-full items-center gap-2 rounded-md bg-muted px-2.5 py-1.5 text-sm font-semibold"
				>
					My files
				</a>
			</nav>
		</aside>
	);
}
