import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
} from "@tanstack/react-router";
import { useState } from "react";
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
import { ErrorBoundary, ErrorPanel } from "./ErrorState";
import { getThemePref, setThemePref, type ThemePref } from "./theme";

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
// UI is up immediately (serve-first startup), pages fill in live as data
// lands (SSE notifies fire during the pass), and the banner carries the
// real counter so the wait never looks hung. It also surfaces a failed
// v1 import: the server retries on every start, but until one succeeds
// the failure must stay visible, not vanish into a startup log line.
function IndexingBanner() {
  const { data } = useQuery({
    queryKey: ["health"],
    queryFn: async () => {
      const res = await fetch("/api/v1/health");
      const body: {
        data?: {
          indexing?: boolean;
          progress?: { agent: string; seen: number; changed: number };
          v1Import?: { state: string; error?: string };
        };
      } = await res.json();
      return body.data ?? {};
    },
    refetchInterval: (query) => (query.state.data?.indexing ? 1500 : false),
  });
  const importFailed = data?.v1Import?.state === "failed";
  if (!data?.indexing && !importFailed) return null;
  const p = data?.progress;
  return (
    <>
      {importFailed && (
        <div className="mb-6 flex items-baseline gap-3 rounded-md border border-red-500/50 bg-surface-1 px-4 py-2 text-sm text-ink-dim">
          <span className="inline-block h-1.5 w-1.5 shrink-0 self-center rounded-full bg-red-500" />
          <span>
            Importing your v1 database failed — its data is not in this index
            yet. Run <code className="font-mono text-ink">ccpeek migrate</code>{" "}
            to retry and see the error.
          </span>
          {data?.v1Import?.error && (
            <span
              className="ml-auto max-w-96 shrink-0 truncate font-mono text-xs text-ink-faint"
              title={data.v1Import.error}
            >
              {data.v1Import.error}
            </span>
          )}
        </div>
      )}
      {data?.indexing && (
        <div className="mb-6 flex items-baseline gap-3 rounded-md border border-accent/40 bg-surface-1 px-4 py-2 text-sm text-ink-dim">
          <span className="inline-block h-1.5 w-1.5 shrink-0 animate-pulse self-center rounded-full bg-accent" />
          <span>
            Indexing your agent history — pages fill in live as data lands.
          </span>
          {p && p.seen > 0 && (
            <span className="ml-auto shrink-0 font-mono text-xs text-ink-faint tabular-nums">
              {p.agent} · {p.seen.toLocaleString()} sources checked ·{" "}
              {p.changed.toLocaleString()} indexed
            </span>
          )}
        </div>
      )}
    </>
  );
}

// ThemeToggle cycles system → light → dark. The preference persists in
// localStorage and applies before hydration (index.html), so it holds
// across restarts without a flash.
const THEME_ORDER: ThemePref[] = ["system", "light", "dark"];
const THEME_GLYPH: Record<ThemePref, string> = {
  system: "◐",
  light: "☀",
  dark: "☾",
};

function ThemeToggle() {
  const [pref, setPref] = useState<ThemePref>(getThemePref);
  const next = THEME_ORDER[(THEME_ORDER.indexOf(pref) + 1) % 3];
  return (
    <button
      onClick={() => {
        setThemePref(next);
        setPref(next);
      }}
      className="microlabel flex items-center gap-2 transition-colors hover:text-ink"
      title={`Theme: ${pref} — click for ${next}`}
    >
      <span aria-hidden className="text-xs leading-none">
        {THEME_GLYPH[pref]}
      </span>
      theme · {pref}
    </button>
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
                className: "border-l-2 border-accent bg-surface-2/70 text-ink",
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
          <ThemeToggle />
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
          {/* Inside the layout, so a failing route keeps the nav rail
              and the user can move on rather than facing a blank page. */}
          <ErrorBoundary>
            <Outlet />
          </ErrorBoundary>
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
    validateSearch: (s: Record<string, unknown>) => {
      const out: { agent?: string; kind?: string } = {};
      if (typeof s.agent === "string" && s.agent !== "") out.agent = s.agent;
      if (typeof s.kind === "string" && s.kind !== "") out.kind = s.kind;
      return out;
    },
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
    validateSearch: (s: Record<string, unknown>) =>
      s.ignored === true ? { ignored: true } : {},
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/compare",
    component: ComparePage,
    validateSearch: (s: Record<string, unknown>) => {
      const out: { a?: string; b?: string } = {};
      if (typeof s.a === "string" && /^[^|]+\|[^|]+$/.test(s.a)) out.a = s.a;
      if (typeof s.b === "string" && /^[^|]+\|[^|]+$/.test(s.b)) out.b = s.b;
      return out;
    },
  }),
]);

export const router = createRouter({
  routeTree,
  // A route that throws during load or render degrades to a message
  // rather than unmounting the app; an unknown path says so instead of
  // rendering nothing.
  defaultErrorComponent: ({ error }) => <ErrorPanel error={error} />,
  defaultNotFoundComponent: () => (
    <div className="rounded-lg border border-edge bg-surface-1 p-5 text-sm">
      <h2 className="mb-1 font-semibold">Nothing here.</h2>
      <p className="text-ink-dim">
        That URL does not match any ccpeek view.{" "}
        <Link to="/" className="text-accent hover:underline">
          Back to the overview
        </Link>
        .
      </p>
    </div>
  ),
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
