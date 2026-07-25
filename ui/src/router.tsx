import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  useNavigate,
  useSearch,
} from "@tanstack/react-router";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { OverviewPage } from "./pages/Overview";
import { SessionsPage } from "./pages/Sessions";
import { SessionDetailPage } from "./pages/SessionDetail";
import { CommandsPage } from "./pages/Commands";
import { UsagePage } from "./pages/Usage";
import { ArtifactsPage } from "./pages/Artifacts";
import { ArtifactDetailPage } from "./pages/ArtifactDetail";
import { ScanPage } from "./pages/Scan";
import { ComparePage } from "./pages/Compare";
import { Palette } from "./Palette";
import { ErrorBoundary, ErrorPanel } from "./ErrorState";
import { getThemePref, setThemePref, type ThemePref } from "./theme";
import { PALETTE_KEY, openPalette } from "./ui";
import { useEffect } from "react";

// The sidebar mirrors the entity map: activity first, then the session
// hub, then what hangs off it. Search is deliberately absent: it is the
// palette (⌘/Ctrl K), reachable from every view, rather than a page you
// must navigate to before you can start looking.
const NAV: { to: string; label: string; exact?: boolean }[] = [
  { to: "/", label: "Overview", exact: true },
  { to: "/sessions", label: "Sessions" },
  { to: "/usage", label: "Usage" },
  { to: "/commands", label: "Commands" },
  { to: "/artifacts", label: "Artifacts" },
  { to: "/scan", label: "Scan" },
  { to: "/compare", label: "Compare" },
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

function NavLinks({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <>
      {NAV.map((n) => (
        <Link
          key={n.to}
          to={n.to}
          onClick={onNavigate}
          activeProps={{
            className: "border-l-2 border-accent bg-surface-2/70 text-ink",
          }}
          inactiveProps={{
            className:
              "border-l-2 border-transparent text-ink-dim hover:bg-surface-2/40 hover:text-ink",
          }}
          activeOptions={{ exact: n.exact ?? false }}
          className="rounded-r px-3 py-1.5 font-mono text-data transition-colors"
        >
          {n.label}
        </Link>
      ))}
    </>
  );
}

function SidebarFooter() {
  return (
    <div className="mt-auto space-y-2 px-4 pb-4">
      <ThemeToggle />
      <div className="microlabel flex items-center gap-2">
        <span className="inline-block h-1.5 w-1.5 rounded-full bg-ok" />
        local · 127.0.0.1
      </div>
      {/* A real button: the hint used to be an inert <kbd> that looked
          exactly like a control, so clicking it did nothing. */}
      <button
        type="button"
        onClick={() => openPalette()}
        className="microlabel rounded border border-edge px-1.5 py-0.5 transition-colors hover:border-edge-strong hover:text-ink"
      >
        {PALETTE_KEY} search
      </button>
    </div>
  );
}

function Layout() {
  const [menu, setMenu] = useState(false);
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
        <nav aria-label="Main" className="flex flex-col gap-0.5 px-2">
          <NavLinks />
        </nav>
        <SidebarFooter />
      </aside>

      <div className="min-w-0 flex-1 md:pl-52">
        <div className="mx-auto max-w-[1500px] px-5 py-5">
          {/* Below md the rail becomes a real disclosure menu. The old
              fallback wrapped every link inline around the logo, which
              reflowed into three ragged rows and marked nothing active. */}
          <div className="mb-4 md:hidden">
            <div className="flex items-center gap-3">
              <Link to="/" className="font-mono text-lg font-semibold">
                <span className="text-accent">cc</span>peek
              </Link>
              <button
                type="button"
                onClick={() => openPalette()}
                aria-label="Search"
                className="ml-auto rounded-md border border-edge px-2 py-1.5 font-mono text-xs text-ink-dim"
              >
                search
              </button>
              <button
                type="button"
                onClick={() => setMenu((v) => !v)}
                aria-expanded={menu}
                aria-controls="mobile-nav"
                className="rounded-md border border-edge px-2 py-1.5 font-mono text-xs text-ink-dim"
              >
                {menu ? "close" : "menu"}
              </button>
            </div>
            {menu && (
              <nav
                id="mobile-nav"
                aria-label="Main"
                className="mt-2 flex flex-col gap-0.5 rounded-md border border-edge bg-surface-1 py-1"
              >
                <NavLinks onNavigate={() => setMenu(false)} />
                <div className="mt-1 border-t border-edge pt-2">
                  <div className="px-4">
                    <ThemeToggle />
                  </div>
                </div>
              </nav>
            )}
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

// pickStrings keeps the non-empty string parameters a route understands
// and drops everything else, so URLs stay clean and an unknown or blank
// value can never reach a query. Each route spelled this out per key —
// eleven near-identical `if (typeof s.x === "string" && s.x !== "")` lines
// whose only interesting property was that none of them differed.
function pickStrings<K extends string>(
  s: Record<string, unknown>,
  keys: readonly K[],
): Partial<Record<K, string>> {
  const out: Partial<Record<K, string>> = {};
  for (const k of keys) {
    const v = s[k];
    if (typeof v === "string" && v !== "") out[k] = v;
  }
  return out;
}

// SearchDoorway opens the palette (carrying any ?q) and steps aside to
// the overview, so the URL never rests on a route with nothing to render.
function SearchDoorway() {
  const search = useSearch({ from: "/search" });
  const navigate = useNavigate();
  useEffect(() => {
    // Deferred by a tick: effects run children-first, and this route is a
    // child of the layout that renders the palette — so a synchronous
    // dispatch fires before the palette has registered its listener and
    // is simply lost.
    // Both deferred by a tick, in this order. Effects run children-first
    // and this route is a child of the layout that renders the palette, so
    // a synchronous dispatch fires before the palette has registered its
    // listener. Navigating first would be worse still: it unmounts this
    // component, and the cleanup would cancel the very dispatch it is
    // waiting on.
    const t = window.setTimeout(() => {
      openPalette(search.q ?? "");
      void navigate({ to: "/", replace: true });
    }, 0);
    return () => window.clearTimeout(t);
  }, [search.q, navigate]);
  return null;
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
    validateSearch: (s: Record<string, unknown>) =>
      pickStrings(s, ["agent", "q", "project", "model", "since", "until"]),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/sessions/$agent/$sessionId",
    component: SessionDetailPage,
    validateSearch: (s: Record<string, unknown>) => {
      // "transcript" is the default tab and stays out of the URL.
      const { tab } = pickStrings(s, ["tab"]);
      const out: { tab?: string; seq?: number } = {};
      if (tab !== undefined && tab !== "transcript") out.tab = tab;
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
  // /search has no page of its own any more — searching is the palette,
  // reachable from every view. The ROUTE survives because the v1
  // compatibility layer still 301s `/search/?q=…` here (see
  // navigation.spec.ts), and a bookmark that lands on "nothing here" is a
  // regression even when the feature moved somewhere better.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/search",
    component: SearchDoorway,
    validateSearch: (s: Record<string, unknown>) => pickStrings(s, ["q"]),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/artifacts",
    component: ArtifactsPage,
    validateSearch: (s: Record<string, unknown>) =>
      pickStrings(s, ["agent", "kind"]),
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
    <div className="rounded-md border border-edge bg-surface-1 p-5 text-sm">
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
