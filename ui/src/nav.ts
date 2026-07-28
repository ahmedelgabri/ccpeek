// NAV is the app's destination list, in the order the sidebar shows it:
// activity first, then the session hub, then what hangs off it. Search is
// deliberately absent — it is the palette (⌘/Ctrl K), reachable from every
// view, rather than a page you must navigate to before you can look.
//
// It lives here rather than in router.tsx because the palette jumps to
// these same destinations, and router.tsx imports the palette — so a
// shared module is what keeps the two lists from being two lists. They had
// already drifted: the palette called /scan "Secret scan".
export const NAV: { to: string; label: string; exact?: boolean }[] = [
  { to: "/", label: "Overview", exact: true },
  { to: "/sessions", label: "Sessions" },
  { to: "/usage", label: "Usage" },
  { to: "/commands", label: "Commands" },
  { to: "/artifacts", label: "Artifacts" },
  { to: "/scan", label: "Scan" },
  { to: "/compare", label: "Compare" },
];
