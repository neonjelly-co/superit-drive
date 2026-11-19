import { Wordmark } from "./wordmark";

/**
 * The left sidebar of layout D. In the full design it holds, from the top: Upload and New folder, My files, the shared
 * folders by name, New shared folder, Shared with me, Trash, and the storage meter at the bottom. Each arrives with the
 * ticket that makes it work; so far there is only "My files".
 */
export function Sidebar({ project }: { project?: string }) {
	return (
		<aside className="flex w-60 shrink-0 flex-col border-r border-border bg-card p-3">
			<div className="px-2.5 py-2">
				<Wordmark className="text-xl" />
				<p className="mt-1 truncate text-sm text-muted-foreground" title={project}>
					Drive{project ? ` · ${project}` : ""}
				</p>
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
