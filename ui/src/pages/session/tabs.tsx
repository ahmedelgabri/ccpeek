import { useMemo, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useHighlight } from "../../highlight";
import { shortPath, type ToolCallRow } from "../../api";
import { CopyButton, EmptyNote, KindBars, toolColor } from "../../ui";
import { JumpButton, ToolExpansion } from "./ToolExpansion";

export function CommandsTab({
  commands,
  onJump,
}: {
  commands: ToolCallRow[];
  onJump: (seq: number) => void;
}) {
  const container = useRef<HTMLDivElement>(null);
  useHighlight(container, [commands]);
  if (commands.length === 0)
    return <EmptyNote>No shell commands in this session.</EmptyNote>;
  return (
    <div ref={container}>
      <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
        {commands.map((c) => (
          <li
            key={c.seq}
            className="flex items-start gap-3 bg-surface-1 px-3 py-2"
          >
            <pre className="min-w-0 flex-1 text-xs leading-relaxed">
              <code className="language-bash block break-words whitespace-pre-wrap">
                {c.detail}
              </code>
            </pre>
            <JumpButton seq={c.messageSeq} onJump={onJump} />
            <span className="shrink-0 font-mono text-[10px] text-ink-faint tabular-nums">
              {c.at?.slice(11, 19)}
            </span>
            <CopyButton text={c.detail ?? ""} />
          </li>
        ))}
      </ul>
    </div>
  );
}

export function ToolsTab({
  agent,
  sessionId,
  tools,
  onJump,
}: {
  agent: string;
  sessionId: string;
  tools: ToolCallRow[];
  onJump: (seq: number) => void;
}) {
  const kinds = useMemo(() => {
    const byKind = new Map<string, number>();
    for (const t of tools) byKind.set(t.kind, (byKind.get(t.kind) ?? 0) + 1);
    return Array.from(byKind, ([label, count]) => ({ label, count })).toSorted(
      (a, b) => b.count - a.count,
    );
  }, [tools]);
  if (tools.length === 0) return <EmptyNote>No tool calls recorded.</EmptyNote>;
  return (
    <div className="space-y-3">
      <div className="rounded-md border border-edge bg-surface-1">
        <div className="microlabel border-b border-edge px-3 py-2">
          Calls by kind
        </div>
        <KindBars items={kinds} />
      </div>
      <div className="overflow-hidden rounded-md border border-edge">
        <table className="w-full text-sm">
          <thead className="bg-surface-2 text-left font-mono text-[10px] tracking-wider text-ink-faint uppercase">
            <tr>
              <th className="px-3 py-1.5">#</th>
              <th className="px-3 py-1.5">tool</th>
              <th className="px-3 py-1.5">kind</th>
              <th className="px-3 py-1.5">detail</th>
              <th className="px-3 py-1.5 text-right">msg</th>
              <th className="px-3 py-1.5 text-right">at</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-edge bg-surface-1">
            {tools.map((t) => (
              <ToolRow
                key={t.seq}
                agent={agent}
                sessionId={sessionId}
                t={t}
                onJump={onJump}
              />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ToolRow({
  agent,
  sessionId,
  t,
  onJump,
}: {
  agent: string;
  sessionId: string;
  t: ToolCallRow;
  onJump: (seq: number) => void;
}) {
  const [open, setOpen] = useState(false);
  const hasDiff = t.kind === "file_edit" || t.kind === "file_write";
  return (
    <>
      <tr
        onClick={hasDiff ? () => setOpen((v) => !v) : undefined}
        className={hasDiff ? "cursor-pointer hover:bg-surface-2/40" : undefined}
      >
        <td className="px-3 py-1.5 font-mono text-xs text-ink-faint tabular-nums">
          {t.seq}
        </td>
        <td className="px-3 py-1.5 font-mono text-xs">{t.name}</td>
        <td className="px-3 py-1.5">
          <span className="inline-flex items-center gap-1.5 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink-dim">
            <span
              aria-hidden
              className="inline-block h-1.5 w-1.5 rounded-full"
              style={{ background: toolColor(t.kind) }}
            />
            {t.kind}
          </span>
        </td>
        <td className="max-w-0 truncate px-3 py-1.5 font-mono text-xs text-ink-dim">
          {hasDiff && (
            <span className="mr-1.5 text-accent">{open ? "▾" : "▸"} diff</span>
          )}
          {t.detail && shortPath(t.detail)}
        </td>
        <td className="px-3 py-1.5 text-right">
          <JumpButton seq={t.messageSeq} onJump={onJump} />
        </td>
        <td className="px-3 py-1.5 text-right font-mono text-[11px] text-ink-faint tabular-nums">
          {t.at?.slice(11, 19)}
        </td>
      </tr>
      {hasDiff && open && (
        <tr>
          <td colSpan={6} className="px-3 py-2">
            <ToolExpansion agent={agent} sessionId={sessionId} seq={t.seq} />
          </td>
        </tr>
      )}
    </>
  );
}

export interface FileGroup {
  path: string;
  reads: number;
  // changes holds every edit/write row; their diff payloads are fetched
  // per row when expanded. The write and edit counts are derived from it
  // rather than tracked alongside — kept as separate fields they had to
  // stay in lockstep by hand, and the sort depended on that equality.
  changes: ToolCallRow[];
}

const countKind = (g: FileGroup, kind: string) =>
  g.changes.reduce((n, t) => (t.kind === kind ? n + 1 : n), 0);

export function groupFiles(tools: ToolCallRow[]): FileGroup[] {
  const map = new Map<string, FileGroup>();
  for (const t of tools) {
    if (!t.detail || !t.kind.startsWith("file_")) continue;
    const g = map.get(t.detail) ?? { path: t.detail, reads: 0, changes: [] };
    if (t.kind === "file_read") g.reads++;
    if (t.kind === "file_edit" || t.kind === "file_write") g.changes.push(t);
    map.set(t.detail, g);
  }
  return Array.from(map.values()).toSorted(
    (a, b) => b.changes.length - a.changes.length,
  );
}

export function FilesTab({
  agent,
  sessionId,
  files,
  onJump,
}: {
  agent: string;
  sessionId: string;
  files: FileGroup[];
  onJump: (seq: number) => void;
}) {
  if (files.length === 0)
    return <EmptyNote>No files touched in this session.</EmptyNote>;
  return (
    <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
      {files.map((f) => (
        <FileRow
          key={f.path}
          agent={agent}
          sessionId={sessionId}
          f={f}
          onJump={onJump}
        />
      ))}
    </ul>
  );
}

function FileRow({
  agent,
  sessionId,
  f,
  onJump,
}: {
  agent: string;
  sessionId: string;
  f: FileGroup;
  onJump: (seq: number) => void;
}) {
  const [open, setOpen] = useState(false);
  const diffs = f.changes;
  const edits = countKind(f, "file_edit");
  const writes = countKind(f, "file_write");
  return (
    <li className="bg-surface-1">
      <div
        onClick={diffs.length > 0 ? () => setOpen((v) => !v) : undefined}
        className={`flex items-baseline gap-3 px-3 py-1.5 ${
          diffs.length > 0 ? "cursor-pointer hover:bg-surface-2/40" : ""
        }`}
      >
        {diffs.length > 0 && (
          <span className="shrink-0 font-mono text-[11px] text-accent">
            {open ? "▾" : "▸"}
          </span>
        )}
        <span className="truncate font-mono text-xs">{shortPath(f.path)}</span>
        <span className="ml-auto flex shrink-0 gap-2 font-mono text-[10px] text-ink-faint tabular-nums">
          {edits > 0 && <span className="text-warn">{edits} edits</span>}
          {writes > 0 && <span className="text-ok">{writes} writes</span>}
          {f.reads > 0 && <span>{f.reads} reads</span>}
        </span>
        <CopyButton text={f.path} />
      </div>
      {open && (
        <div className="space-y-2 border-t border-edge px-3 py-2">
          {diffs.map((e) => (
            <div key={e.seq}>
              <div className="mb-1 flex items-center gap-2 font-mono text-[10px] text-ink-faint tabular-nums">
                <span>
                  {e.kind === "file_write" ? "write" : "edit"} #{e.seq} ·{" "}
                  {e.at?.slice(11, 19)}
                </span>
                <JumpButton seq={e.messageSeq} onJump={onJump} />
              </div>
              <ToolExpansion agent={agent} sessionId={sessionId} seq={e.seq} />
            </div>
          ))}
        </div>
      )}
    </li>
  );
}

export function ArtifactsTab({
  agent,
  artifacts,
}: {
  agent: string;
  artifacts: {
    kind: string;
    name: string;
    relation: string;
    evidence: string;
  }[];
}) {
  if (artifacts.length === 0)
    return <EmptyNote>No artifacts linked to this session.</EmptyNote>;
  return (
    <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
      {artifacts.map((a) => (
        <li key={`${a.kind}-${a.name}`}>
          <Link
            to="/artifacts/$agent/$kind/$name"
            params={{ agent, kind: a.kind, name: a.name }}
            className="flex items-baseline gap-3 bg-surface-1 px-3 py-2 transition-colors hover:bg-surface-2/40"
          >
            <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-accent">
              {a.kind.replaceAll("_", " ")}
            </span>
            <span className="truncate font-mono text-xs">{a.name}</span>
            <span className="ml-auto shrink-0 font-mono text-[10px] text-ink-faint">
              {a.relation} · {a.evidence}
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
