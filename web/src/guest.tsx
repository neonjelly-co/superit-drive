// The guests' page: one page under the public prefix (/s/guest/) that signs a guest in with a code mailed to them and
// shows what was shared with them. It calls the guests' own API (forGuests) and never the members'.
import "./styles.css";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { forGuests } from "./api";
import { GuestApp } from "./guest-app";
import { start } from "./i18n";

forGuests();

const queryClient = new QueryClient({
	defaultOptions: { queries: { retry: false } },
});

const root = document.getElementById("root");
if (!root) throw new Error("guest.html has no #root");

// The page's words first, then the page: it is never shown in a language it is not in.
void start().then(() =>
	createRoot(root).render(
		<StrictMode>
			<QueryClientProvider client={queryClient}>
				<GuestApp />
			</QueryClientProvider>
		</StrictMode>,
	),
);
