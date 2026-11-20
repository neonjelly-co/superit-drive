import "./styles.css";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./app";
import { start } from "./i18n";

const queryClient = new QueryClient({
	defaultOptions: { queries: { retry: 1 } },
});

const root = document.getElementById("root");
if (!root) throw new Error("index.html has no #root");

// The page's words first, then the page: it is never shown in a language it is not in.
void start().then(() =>
	createRoot(root).render(
		<StrictMode>
			<QueryClientProvider client={queryClient}>
				<App />
			</QueryClientProvider>
		</StrictMode>,
	),
);
