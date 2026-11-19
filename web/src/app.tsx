import { useQuery } from "@tanstack/react-query";
import { Upload } from "lucide-react";
import { type DragEvent, useEffect, useState } from "react";
import { api } from "./api";
import { DetailsPanel } from "./components/details-panel";
import { ItemList } from "./components/item-list";
import { Sidebar } from "./components/sidebar";
import { UploadsPanel } from "./components/uploads-panel";
import { droppedFiles, useUploads } from "./uploads";

const carriesFiles = (e: { dataTransfer: DataTransfer | null }) => e.dataTransfer?.types.includes("Files") ?? false;

/**
 * The Drive as a member sees it: layout D of the prototype. A sidebar on the left, the list in the middle, and the
 * details panel on the right, which opens when one item is selected. Files are uploaded with the sidebar's Upload
 * button or by dragging them onto the list.
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
	const uploads = useUploads(myFiles?.id, items.data);
	const canUpload = myFiles !== undefined;
	const [dragging, setDragging] = useState(false);

	const project = me.data?.project;
	useEffect(() => {
		document.title = project ? `My files · ${project}` : "My files";
	}, [project]);

	// A file dropped anywhere but the list would make the browser leave the Drive to show it. Refuse those drops.
	useEffect(() => {
		const refuse = (e: globalThis.DragEvent) => {
			if (!carriesFiles(e) || e.defaultPrevented) return;
			e.preventDefault();
			if (e.dataTransfer) e.dataTransfer.dropEffect = "none";
		};
		window.addEventListener("dragover", refuse);
		window.addEventListener("drop", refuse);
		return () => {
			window.removeEventListener("dragover", refuse);
			window.removeEventListener("drop", refuse);
		};
	}, []);

	const dragOver = (e: DragEvent) => {
		if (!carriesFiles(e) || !canUpload) return;
		e.preventDefault();
		e.dataTransfer.dropEffect = "copy";
		setDragging(true);
	};

	return (
		<div className="flex h-screen bg-background text-foreground">
			<Sidebar project={project} onUpload={canUpload ? (files) => uploads.add(files.map((file) => ({ file }))) : undefined} />
			<div
				className="relative min-w-0 flex-1"
				onDragEnter={dragOver}
				onDragOver={dragOver}
				onDragLeave={(e) => {
					if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
				}}
				onDrop={(e) => {
					if (!carriesFiles(e) || !canUpload) return;
					e.preventDefault();
					setDragging(false);
					uploads.add(droppedFiles(e.dataTransfer));
				}}
			>
				<main className="h-full overflow-auto p-6 pb-24">
					<h1 className="text-xl font-semibold">My files</h1>
					<p className="mt-1 text-sm text-muted-foreground">
						Only you can see these, unless you share them. Project admins cannot open them.
					</p>
					<ItemList
						placeId={myFiles?.id}
						items={items.data}
						loading={places.isPending || items.isPending}
						error={places.error ?? items.error}
						onRetry={() => (places.error ? places.refetch() : items.refetch())}
					/>
				</main>
				{dragging && (
					<div className="pointer-events-none absolute inset-3 grid place-items-center rounded-xl border-2 border-dashed border-ring bg-card/85">
						<p className="flex items-center gap-2 font-semibold">
							<Upload aria-hidden className="size-5 text-muted-foreground" /> Drop files to upload them to My files
						</p>
					</div>
				)}
				<UploadsPanel uploads={uploads.uploads} onClose={uploads.clear} />
			</div>
			<DetailsPanel item={null} />
		</div>
	);
}
