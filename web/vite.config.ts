import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { readFileSync } from "node:fs";
import { defineConfig, type Plugin } from "vite";

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

// The built interface holds other people's software (React, the icons, the fonts), whose licences ask that their
// notices go wherever a copy goes, and a browser is sent a copy. So the build puts the repository's
// THIRD-PARTY-NOTICES.txt next to the interface's files, and every script starts with a line that names it. (The
// minifier takes the packages' own licence comments out, and would take this one out of a chunk not yet minified, so
// it is added to the finished files.)
function notices(): Plugin {
	const fileName = "assets/THIRD-PARTY-NOTICES.txt";
	return {
		name: "third-party-notices",
		apply: "build",
		buildStart() {
			this.emitFile({ type: "asset", fileName, source: readFileSync(`${import.meta.dirname}/../THIRD-PARTY-NOTICES.txt`) });
		},
		generateBundle(_, bundle) {
			for (const file of Object.values(bundle)) {
				if (file.type === "chunk") {
					file.code = `/*! Superit Drive, AGPL-3.0. The notices and licences of the software in it: ./THIRD-PARTY-NOTICES.txt */\n${file.code}`;
				}
			}
		},
	};
}

export default defineConfig({
	plugins: [react(), tailwindcss(), notices()],
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
