import { lazy, Suspense, useRef } from "react";
import { LoadError } from "../../ui";
import { useQuery } from "@tanstack/react-query";
import { useHighlight } from "../../highlight";
import { api } from "../../api";
import { formatScript } from "../../formatScript";

// Lazy so @pierre/diffs ships as its own chunk, loaded on the first
// opened file change.
const DiffView = lazy(() =>
  import("../../Diff").then((m) => ({ default: m.DiffView })),
);

// Kinds whose detail is source code rather than a path.
const CODE_LANGUAGE: Record<string, string> = {
  shell: "language-bash",
  script: "language-javascript",
};

export function isCodeKind(kind: string): boolean {
  return Object.hasOwn(CODE_LANGUAGE, kind);
}

// ToolExpansion mounts only when a chip or row is opened: opening is
// what fetches the full payload — the 16 KiB diff excerpts never ride
// list responses.
export function ToolExpansion({
  agent,
  sessionId,
  seq,
}: {
  agent: string;
  sessionId: string;
  seq: number;
}) {
  const q = useQuery({
    queryKey: ["tooldetail", agent, sessionId, seq],
    queryFn: () => api.sessionToolDetail(agent, sessionId, seq),
  });
  // The payload highlights ITSELF, keyed on the answer that arrives. Its
  // three hosts — the transcript's chips, the tools grid, the files tab —
  // each ran (or did not run) their own pass over a container, and none of
  // them could have known when this fetch resolved, so a shell command
  // opened in the tools or files tab stayed unstyled.
  const isScript = q.data?.kind === "script";
  const script = useQuery({
    queryKey: ["toolscript", agent, sessionId, seq],
    queryFn: () => formatScript(q.data?.detail ?? ""),
    enabled: isScript,
    staleTime: Infinity,
  });
  const body = useRef<HTMLDivElement>(null);
  useHighlight(body, [q.data, script.data]);
  // A script renders only once formatted: the highlighter rewrites the
  // block in place, so swapping its text afterwards would fight React.
  if (q.isLoading || (isScript && script.isPending))
    return <p className="font-mono text-meta text-ink-dim">Loading…</p>;
  const d = q.data;
  if (!d) return <LoadError error={q.error ?? "not found"} compact />;
  if ((d.kind === "file_edit" || d.kind === "file_write") && (d.old || d.new)) {
    // For file kinds detail IS the path (the list query coalesces
    // command → file_path), which is the new-file view's language signal.
    return (
      <Suspense
        fallback={<p className="font-mono text-meta text-ink-dim">Loading…</p>}
      >
        <DiffView old={d.old ?? ""} new={d.new ?? ""} path={d.detail} />
      </Suspense>
    );
  }
  return (
    <div ref={body}>
      <pre
        className={`${isScript ? "max-h-[32rem]" : "max-h-64"} overflow-auto rounded-md border border-edge bg-surface px-3 py-2 text-meta leading-relaxed`}
      >
        <code
          className={`block whitespace-pre-wrap ${CODE_LANGUAGE[d.kind] ?? ""}`}
        >
          {isScript ? (script.data ?? d.detail) : d.detail}
        </code>
      </pre>
    </div>
  );
}
// JumpButton links a tool call / file change back to the message that
// issued it: it navigates the transcript to that seq, where the shared
// anchoring loads the window around it and scrolls it into view.
export function JumpButton({
  seq,
  onJump,
}: {
  seq: number;
  onJump: (seq: number) => void;
}) {
  return (
    <button
      onClick={(e) => {
        e.stopPropagation();
        onJump(seq);
      }}
      title={`Jump to message #${seq} in the transcript`}
      className="shrink-0 font-mono text-micro text-ink-faint tabular-nums hover:text-accent"
    >
      ↗ #{seq}
    </button>
  );
}
