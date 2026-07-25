import { LoadError } from "../../ui";
import { useQuery } from "@tanstack/react-query";
import { DiffView } from "../../Diff";
import { api } from "../../api";

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
  if (q.isLoading)
    return <p className="font-mono text-[11px] text-ink-dim">Loading…</p>;
  const d = q.data;
  if (!d) return <LoadError error={q.error ?? "not found"} compact />;
  if ((d.kind === "file_edit" || d.kind === "file_write") && (d.old || d.new)) {
    return <DiffView old={d.old ?? ""} new={d.new ?? ""} />;
  }
  return (
    <pre className="max-h-64 overflow-auto rounded-md border border-edge bg-surface px-3 py-2 text-[11px] leading-relaxed">
      <code
        className={
          d.kind === "shell"
            ? "language-bash block whitespace-pre-wrap"
            : "block whitespace-pre-wrap"
        }
      >
        {d.detail}
      </code>
    </pre>
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
      className="shrink-0 font-mono text-[10px] text-ink-faint tabular-nums hover:text-accent"
    >
      ↗ #{seq}
    </button>
  );
}
