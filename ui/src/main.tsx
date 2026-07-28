import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { router } from "./router";
import { ErrorBoundary } from "./ErrorState";
import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 15_000, retry: 1 },
  },
});

// Live updates (§5.5): the server pushes "changed" over SSE whenever a
// re-index landed new data. 501 (watch off) simply means no live events.
//
// Two bounds on what an event costs. Only ACTIVE queries refetch —
// invalidating the whole cache also woke every view the user had merely
// visited. And the handler coalesces: during the initial index pass the
// server notifies every two seconds for its whole duration (minutes on a
// large history), so an unthrottled handler refetched everything on
// screen that often, against a database already saturated by ingest.
// Above the server's own notify floor (one every 2s during indexing, see
// internal/cmd/root.go) — at exactly that floor each event lands after the
// previous timer fired and nothing coalesced.
const REFRESH_COALESCE_MS = 5000;
let refreshTimer: number | undefined;

const events = new EventSource("/api/v1/events");
events.addEventListener("changed", () => {
  if (refreshTimer !== undefined) return;
  refreshTimer = window.setTimeout(() => {
    refreshTimer = undefined;
    void queryClient.invalidateQueries({ refetchType: "active" });
  }, REFRESH_COALESCE_MS);
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {/* The outermost floor: the router has its own error component for
        route failures, this catches anything above or around it. */}
    <ErrorBoundary scope="ccpeek">
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </ErrorBoundary>
  </StrictMode>,
);
