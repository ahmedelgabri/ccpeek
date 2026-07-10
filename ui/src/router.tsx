import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
} from "@tanstack/react-router";
import { SessionsPage } from "./pages/Sessions";
import { SessionDetailPage } from "./pages/SessionDetail";
import { UsagePage } from "./pages/Usage";

function Layout() {
  return (
    <div className="mx-auto max-w-6xl px-4 py-6">
      <header className="mb-8 flex items-center gap-6">
        <Link to="/" className="text-lg font-semibold tracking-tight">
          <span className="text-accent">cc</span>peek
        </Link>
        <nav className="flex gap-4 text-sm text-ink-dim">
          <Link
            to="/"
            className="hover:text-ink"
            activeProps={{ className: "text-ink" }}
            activeOptions={{ exact: true }}
          >
            Sessions
          </Link>
          <Link
            to="/usage"
            className="hover:text-ink"
            activeProps={{ className: "text-ink" }}
          >
            Usage
          </Link>
        </nav>
      </header>
      <Outlet />
    </div>
  );
}

const rootRoute = createRootRoute({ component: Layout });

const sessionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: SessionsPage,
});

// Session-centric URLs (docs/v2-plan.md §8.2): /sessions/$agent/$sessionId.
const sessionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/sessions/$agent/$sessionId",
  component: SessionDetailPage,
});

const usageRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/usage",
  component: UsagePage,
});

const routeTree = rootRoute.addChildren([sessionsRoute, sessionRoute, usageRoute]);

export const router = createRouter({
  routeTree,
  basepath: "/v2",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
