import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { api } from "./api";
import { DetailsPanel } from "./components/details-panel";
import { ItemList } from "./components/item-list";
import { Sidebar } from "./components/sidebar";

/**
 * The Drive as a member sees it: layout D of the prototype. A sidebar on the left, the list in the middle, and the
 * details panel on the right, which opens when one item is selected.
 */
export function App() {
	const me = useQuery({ queryKey: ["me"], queryFn: api.me });
	const places = useQuery({ queryKey: ["places"], queryFn: api.places });
	const myFiles = places.data?.find((p) => p.kind === "my-files");
	const items = useQuery({
		queryKey: ["items", myFiles?.id],
		queryFn: () => api.items(myFiles!.id),
		enabled: myFiles !== undefined,
	});

	const project = me.data?.project;
	useEffect(() => {
		document.title = project ? `My files · ${project}` : "My files";
	}, [project]);

	return (
		<div className="flex h-screen bg-background text-foreground">
			<Sidebar project={project} />
			<main className="min-w-0 flex-1 overflow-auto p-6 pb-24">
				<h1 className="text-xl font-semibold">My files</h1>
				<p className="mt-1 text-sm text-muted-foreground">
					Only you can see these, unless you share them. Project admins cannot open them.
				</p>
				<ItemList
					items={items.data}
					loading={places.isPending || items.isPending}
					error={places.error ?? items.error}
					onRetry={() => (places.error ? places.refetch() : items.refetch())}
				/>
			</main>
			<DetailsPanel item={null} />
		</div>
	);
}
