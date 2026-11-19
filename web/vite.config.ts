import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// `bun run dev` serves the interface with hot reload and passes /api to a Drive running on DRIVE_URL (default
// http://127.0.0.1:8080). It adds the headers the reverse proxy would add, signed in as DEV_USER_ID, so the Drive
// lets the requests in. This is for development only; it never ships.
const drive = process.env.DRIVE_URL ?? "http://127.0.0.1:8080";
const devHeaders = {
	"Drive-Secret": process.env.DRIVE_HEADER_SECRET ?? "",
	"Remote-User-Id": process.env.DEV_USER_ID ?? "",
};

export default defineConfig({
	plugins: [react(), tailwindcss()],
	build: { outDir: "dist", emptyOutDir: true },
	server: {
		proxy: { "/api": { target: drive, headers: devHeaders } },
	},
});
