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
import { SearchPage } from "./pages/Search";
import { ArtifactsPage } from "./pages/Artifacts";
import { ArtifactDetailPage } from "./pages/ArtifactDetail";
import { ScanPage } from "./pages/Scan";
import { ComparePage } from "./pages/Compare";
import { Palette } from "./Palette";

const NAV = [
  { to: "/", label: "Sessions", exact: true },
  { to: "/usage", label: "Usage" },
  { to: "/artifacts", label: "Artifacts" },
  { to: "/scan", label: "Scan" },
  { to: "/compare", label: "Compare" },
  { to: "/search", label: "Search" },
] as const;

function Layout() {
  return (
    <div className="mx-auto max-w-6xl px-4 py-6">
      <header className="mb-8 flex items-center gap-6">
        <Link to="/" className="text-lg font-semibold tracking-tight">
          <span className="text-accent">cc</span>peek
        </Link>
        <nav className="flex flex-wrap gap-4 text-sm text-ink-dim">
          {NAV.map((n) => (
            <Link
              key={n.to}
              to={n.to}
              className="hover:text-ink"
              activeProps={{ className: "text-ink" }}
              activeOptions={{ exact: "exact" in n && n.exact }}
            >
              {n.label}
            </Link>
          ))}
        </nav>
        <kbd className="ml-auto hidden rounded border border-edge px-1.5 py-0.5 text-xs text-ink-dim sm:block">
          ⌘K
        </kbd>
      </header>
      <Outlet />
      <Palette />
    </div>
  );
}

const rootRoute = createRootRoute({ component: Layout });

// Session-centric URLs (docs/v2-plan.md §8.2): /sessions/$agent/$sessionId.
const routeTree = rootRoute.addChildren([
  createRoute({ getParentRoute: () => rootRoute, path: "/", component: SessionsPage }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/sessions/$agent/$sessionId",
    component: SessionDetailPage,
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/usage", component: UsagePage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/search", component: SearchPage }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/artifacts",
    component: ArtifactsPage,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/artifacts/$agent/$kind/$name",
    component: ArtifactDetailPage,
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/scan", component: ScanPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/compare", component: ComparePage }),
]);

export const router = createRouter({
  routeTree,
  basepath: "/v2",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
