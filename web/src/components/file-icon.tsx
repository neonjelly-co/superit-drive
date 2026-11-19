import { File, FileArchive, FileAudio, FileImage, FileSpreadsheet, FileText, FileVideo, Folder } from "lucide-react";
import type { Item } from "../api";

const BY_EXTENSION: Record<string, typeof File> = {};
for (const [icon, extensions] of [
	[FileText, "pdf doc docx odt rtf txt md pages key ppt pptx odp"],
	[FileImage, "jpg jpeg png gif webp heic heif avif bmp tif tiff svg"],
	[FileSpreadsheet, "xls xlsx ods csv numbers"],
	[FileArchive, "zip 7z rar tar gz tgz bz2 xz"],
	[FileVideo, "mp4 mov webm mkv avi m4v"],
	[FileAudio, "mp3 wav m4a flac ogg opus aac"],
] as const) {
	for (const ext of extensions.split(" ")) BY_EXTENSION[ext] = icon;
}

/** The icon for a file or folder: by kind, and for files by the extension of the name. */
export function FileIcon({ item, className = "size-5" }: { item: Item; className?: string }) {
	const extension = item.name.includes(".") ? (item.name.split(".").pop() ?? "").toLowerCase() : "";
	const Icon = item.kind === "folder" ? Folder : (BY_EXTENSION[extension] ?? File);
	return <Icon aria-hidden className={`${className} shrink-0 text-muted-foreground`} />;
}
