import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
} from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { OverviewPage } from "./pages/Overview";
import { SessionsPage } from "./pages/Sessions";
import { SessionDetailPage } from "./pages/SessionDetail";
import { CommandsPage } from "./pages/Commands";
import { UsagePage } from "./pages/Usage";
import { SearchPage } from "./pages/Search";
import { ArtifactsPage } from "./pages/Artifacts";
import { ArtifactDetailPage } from "./pages/ArtifactDetail";
import { ScanPage } from "./pages/Scan";
import { ComparePage } from "./pages/Compare";
import { Palette } from "./Palette";

// The sidebar mirrors the entity map: activity first, then the session
// hub, then what hangs off it.
const NAV: { to: string; label: string; exact?: boolean }[] = [
  { to: "/", label: "Overview", exact: true },
  { to: "/sessions", label: "Sessions" },
  { to: "/commands", label: "Commands" },
  { to: "/artifacts", label: "Artifacts" },
  { to: "/usage", label: "Usage" },
  { to: "/scan", label: "Scan" },
  { to: "/compare", label: "Compare" },
  { to: "/search", label: "Search" },
];

// IndexingBanner shows while the server's initial index pass runs — the
// UI is up immediately (serve-first startup) and fills in live once data
// lands, so empty pages need the explanation.
function IndexingBanner() {
  const { data } = useQuery({
    queryKey: ["health"],
    queryFn: async () => {
      const res = await fetch("/api/v1/health");
      const body = (await res.json()) as { data?: { indexing?: boolean } };
      return body.data ?? {};
    },
    refetchInterval: (query) => (query.state.data?.indexing ? 1500 : false),
  });
  if (!data?.indexing) return null;
  return (
    <div className="mb-6 rounded-md border border-accent/40 bg-surface-1 px-4 py-2 text-sm text-ink-dim">
      Indexing your agent history — the first pass over a large corpus can take
      a few minutes. Pages fill in live when it finishes.
    </div>
  );
}

function Layout() {
  return (
    <div className="flex min-h-screen">
      <aside className="fixed inset-y-0 left-0 hidden w-52 flex-col border-r border-edge bg-surface-1/60 md:flex">
        <Link to="/" className="flex items-baseline gap-0 px-4 pt-5 pb-4">
          <span className="font-mono text-lg font-semibold tracking-tight text-accent">
            cc
          </span>
          <span className="font-mono text-lg font-semibold tracking-tight">
            peek
          </span>
        </Link>
        <nav className="flex flex-col gap-0.5 px-2">
          {NAV.map((n) => (
            <Link
              key={n.to}
              to={n.to}
              activeProps={{
                className:
                  "border-l-2 border-accent bg-surface-2/70 text-ink",
              }}
              inactiveProps={{
                className:
                  "border-l-2 border-transparent text-ink-dim hover:bg-surface-2/40 hover:text-ink",
              }}
              activeOptions={{ exact: n.exact ?? false }}
              className="rounded-r px-3 py-1.5 font-mono text-[13px] transition-colors"
            >
              {n.label}
            </Link>
          ))}
        </nav>
        <div className="mt-auto space-y-2 px-4 pb-4">
          <div className="microlabel flex items-center gap-2">
            <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-ok" />
            local · 127.0.0.1
          </div>
          <kbd className="microlabel rounded border border-edge px-1.5 py-0.5">
            ⌘K search
          </kbd>
        </div>
      </aside>

      <div className="min-w-0 flex-1 md:pl-52">
        <div className="mx-auto max-w-[1500px] px-5 py-5">
          <div className="mb-4 flex items-center gap-4 md:hidden">
            <Link to="/" className="font-mono text-lg font-semibold">
              <span className="text-accent">cc</span>peek
            </Link>
            <nav className="flex flex-wrap gap-3 font-mono text-xs text-ink-dim">
              {NAV.map((n) => (
                <Link key={n.to} to={n.to} className="hover:text-ink">
                  {n.label}
                </Link>
              ))}
            </nav>
          </div>
          <IndexingBanner />
          <Outlet />
        </div>
      </div>
      <Palette />
    </div>
  );
}

const rootRoute = createRootRoute({ component: Layout });

// Session-centric URLs (docs/v2-plan.md §8.2): /sessions/$agent/$sessionId.
const routeTree = rootRoute.addChildren([
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: OverviewPage,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/sessions",
    component: SessionsPage,
    // Only non-empty filters serialize, keeping /sessions URLs clean.
    validateSearch: (s: Record<string, unknown>) => {
      const out: {
        agent?: string;
        q?: string;
        project?: string;
        model?: string;
        since?: string;
        until?: string;
      } = {};
      if (typeof s.agent === "string" && s.agent !== "") out.agent = s.agent;
      if (typeof s.model === "string" && s.model !== "") out.model = s.model;
      if (typeof s.q === "string" && s.q !== "") out.q = s.q;
      if (typeof s.project === "string" && s.project !== "")
        out.project = s.project;
      if (typeof s.since === "string" && s.since !== "") out.since = s.since;
      if (typeof s.until === "string" && s.until !== "") out.until = s.until;
      return out;
    },
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/sessions/$agent/$sessionId",
    component: SessionDetailPage,
    validateSearch: (s: Record<string, unknown>) => {
      const out: { tab?: string; seq?: number } = {};
      if (typeof s.tab === "string" && s.tab !== "" && s.tab !== "transcript")
        out.tab = s.tab;
      if (typeof s.seq === "number") out.seq = s.seq;
      return out;
    },
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/commands",
    component: CommandsPage,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/usage",
    component: UsagePage,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/search",
    component: SearchPage,
  }),
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
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/scan",
    component: ScanPage,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/compare",
    component: ComparePage,
  }),
]);

export const router = createRouter({
  routeTree,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
