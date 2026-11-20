import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// `bun run dev` serves the interface with hot reload and passes /api to a Drive running on DRIVE_URL (default
// http://127.0.0.1:8080). It adds the headers the reverse proxy would add, signed in as DEV_USER_ID, so the Drive
// lets the requests in. This is for development only; it never ships. The guests' page is at /guest.html, and its
// calls (/s/) go to the Drive as they are, without those headers.
//
// Two pages are built: the members' (index.html, served at /) and the guests' (guest.html, served at /s/guest/). Their
// files are found relative to the page (base "./"): at /assets/ for members, and at /s/guest/assets/ for guests, which
// the reverse proxy lets through without signing in.
const drive = process.env.DRIVE_URL ?? "http://127.0.0.1:8080";
const devHeaders = {
	"Drive-Secret": process.env.DRIVE_HEADER_SECRET ?? "",
	"Remote-User-Id": process.env.DEV_USER_ID ?? "",
};

export default defineConfig({
	plugins: [react(), tailwindcss()],
	base: "./",
	build: {
		outDir: "dist",
		emptyOutDir: true,
		rollupOptions: { input: { main: `${import.meta.dirname}/index.html`, guest: `${import.meta.dirname}/guest.html` } },
	},
	server: {
		proxy: { "/api": { target: drive, headers: devHeaders }, "/s/": { target: drive } },
	},
});
