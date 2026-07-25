// A floor under render-time failures.
//
// The app is StrictMode > QueryClientProvider > RouterProvider with no
// boundary anywhere, so React unmounted the whole tree on any uncaught
// render error: one unexpected payload — a malformed metadata blob, a
// null the types promised was non-null — turned the UI white with the
// reason visible only in the console. Query errors were always handled;
// render errors had nothing.

import { Component, type ErrorInfo, type ReactNode } from "react";

function describe(error: unknown): string {
  if (error instanceof Error) return error.message || error.name;
  return String(error);
}

/** ErrorPanel is the visible failure state, shared by the boundary and
 *  the router's error component so both failures look the same. */
export function ErrorPanel({
  error,
  scope = "this page",
}: {
  error: unknown;
  scope?: string;
}) {
  return (
    <div
      role="alert"
      className="rounded-lg border border-warn/40 bg-surface-1 p-5 text-sm"
    >
      <h2 className="mb-1 font-semibold text-ink">
        Something went wrong rendering {scope}.
      </h2>
      <p className="mb-3 max-w-prose text-ink-dim">
        The rest of ccpeek still works — use the navigation to move on, or
        reload to try again. Your indexed data is unaffected.
      </p>
      <pre className="mb-3 max-h-40 overflow-auto rounded-md border border-edge bg-surface px-3 py-2 font-mono text-xs text-ink-dim">
        {describe(error)}
      </pre>
      <button
        type="button"
        onClick={() => window.location.reload()}
        className="rounded-md border border-edge bg-surface-2 px-3 py-1.5 font-mono text-xs text-accent hover:bg-surface-2/70"
      >
        Reload
      </button>
    </div>
  );
}

interface Props {
  children: ReactNode;
  scope?: string;
}

interface State {
  error: unknown;
}

/** ErrorBoundary catches render errors below it and shows ErrorPanel
 *  instead of unmounting the tree. */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: unknown): State {
    return { error };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    // Keep the component stack in the console for debugging; the panel
    // shows only the message.
    console.error("ccpeek: render error", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return <ErrorPanel error={this.state.error} scope={this.props.scope} />;
    }
    return this.props.children;
  }
}
