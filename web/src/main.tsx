import "./styles.css";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./app";

const queryClient = new QueryClient({
	defaultOptions: { queries: { retry: 1 } },
});

const root = document.getElementById("root");
if (!root) throw new Error("index.html has no #root");

createRoot(root).render(
	<StrictMode>
		<QueryClientProvider client={queryClient}>
			<App />
		</QueryClientProvider>
	</StrictMode>,
);
