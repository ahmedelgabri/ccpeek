import { useMemo, type KeyboardEvent } from "react";
import { WindowedList } from "../../windowed";
import { Link } from "@tanstack/react-router";
import {
  clipCommand,
  clipPath,
  fmtCount,
  plural,
  shortPath,
  type ToolCallRow,
} from "../../api";
import { fullWhen, localClock } from "../../time";
import {
  CopyButton,
  EmptyNote,
  KindBars,
  kindLabel,
  onDisclosureKey,
  Panel,
  toolColor,
  useToggleSet,
} from "../../ui";

import { JumpButton, ToolExpansion } from "./ToolExpansion";

export function CommandsTab({
  commands,
  onJump,
}: {
  commands: ToolCallRow[];
  onJump: (seq: number) => void;
}) {
  if (commands.length === 0)
    return <EmptyNote>No shell commands in this session.</EmptyNote>;
  return (
    <div className="overflow-hidden rounded-md border border-edge">
      {/* Highlighting follows the WINDOW, not the command list. Rows mount
          lazily, so a pass keyed on the full list ran once against the
          first screenful and left every command below the fold unstyled —
          the global Commands page has always keyed its pass on the visible
          slice. */}
      <WindowedList
        items={commands}
        getKey={(c) => c.seq}
        estimateSize={38}
        className="bg-surface-1"
        highlight
      >
        {(c) => (
          <div className="flex items-start gap-3 border-b border-edge bg-surface-1 px-3 py-2">
            <pre className="min-w-0 flex-1 text-xs leading-relaxed">
              <code className="language-bash block break-words whitespace-pre-wrap">
                {c.detail}
              </code>
            </pre>
            <JumpButton seq={c.messageSeq} onJump={onJump} />
            <span
              title={fullWhen(c.at ?? "")}
              className="shrink-0 font-mono text-micro text-ink-faint tabular-nums"
            >
              {localClock(c.at ?? "")}
            </span>
            <CopyButton text={c.detail ?? ""} />
          </div>
        )}
      </WindowedList>
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
  // Which rows are expanded, held HERE rather than in the row: rows are
  // virtualized, and state inside one is thrown away the moment it scrolls
  // out of the window — an open diff closed itself and refetched on the way
  // back.
  const [open, toggle] = useToggleSet<number>();
  if (tools.length === 0) return <EmptyNote>No tool calls recorded.</EmptyNote>;
  return (
    <div className="space-y-3">
      <Panel label="Calls by kind">
        <KindBars items={kinds} fmt={fmtCount} />
      </Panel>
      {/* A grid rather than a <table>: windowed rows have to be
          absolutely positioned, which a table layout cannot express.
          ARIA roles keep the semantics a table gave for free. */}
      <div
        role="table"
        aria-label="Tool calls"
        aria-rowcount={tools.length}
        className="overflow-hidden rounded-md border border-edge text-sm"
      >
        <div
          role="row"
          className={`${TOOL_COLUMNS} bg-surface-2 font-mono text-micro tracking-wider text-ink-faint uppercase`}
        >
          <span role="columnheader" className="px-3 py-1.5">
            call
          </span>
          <span role="columnheader" className="px-3 py-1.5">
            tool
          </span>
          <span role="columnheader" className="px-3 py-1.5">
            kind
          </span>
          <span role="columnheader" className="px-3 py-1.5">
            detail
          </span>
          <span role="columnheader" className="px-3 py-1.5 text-right">
            msg
          </span>
          <span role="columnheader" className="px-3 py-1.5 text-right">
            at
          </span>
        </div>
        <WindowedList
          items={tools}
          getKey={(t) => t.seq}
          estimateSize={30}
          className="bg-surface-1"
        >
          {(t) => (
            <ToolRow
              agent={agent}
              sessionId={sessionId}
              t={t}
              open={open.has(t.seq)}
              onToggle={() => toggle(t.seq)}
              onJump={onJump}
            />
          )}
        </WindowedList>
      </div>
    </div>
  );
}

// The tool grid's column track, shared by the header and every row so they
// stay aligned without a table.
const TOOL_COLUMNS =
  "grid grid-cols-[4rem_8rem_7rem_minmax(0,1fr)_5rem_5rem] items-baseline";

function ToolRow({
  agent,
  sessionId,
  t,
  open,
  onToggle,
  onJump,
}: {
  agent: string;
  sessionId: string;
  t: ToolCallRow;
  open: boolean;
  onToggle: () => void;
  onJump: (seq: number) => void;
}) {
  const hasDiff = t.kind === "file_edit" || t.kind === "file_write";
  return (
    <div className="border-b border-edge">
      {/* Focusable and operable from the keyboard when it opens something:
          the row cannot be a <button> (it holds the jump control), so it
          takes the tab stop and the enter/space keys itself. */}
      <div
        role="row"
        {...(hasDiff
          ? {
              tabIndex: 0,
              "aria-expanded": open,
              onClick: onToggle,
              onKeyDown: (e: KeyboardEvent<HTMLElement>) =>
                onDisclosureKey(e, onToggle),
            }
          : {})}
        className={`${TOOL_COLUMNS} ${hasDiff ? "cursor-pointer hover:bg-surface-2/40" : ""}`}
      >
        <span
          role="cell"
          className="px-3 py-1.5 font-mono text-xs text-ink-faint tabular-nums"
        >
          {t.seq}
        </span>
        <span role="cell" className="truncate px-3 py-1.5 font-mono text-xs">
          {t.name}
        </span>
        <span role="cell" className="px-3 py-1.5">
          <span className="inline-flex items-center gap-1.5 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-micro text-ink-dim">
            <span
              aria-hidden
              className="inline-block h-1.5 w-1.5 rounded-full"
              style={{ background: toolColor(t.kind) }}
            />
            {kindLabel(t.kind)}
          </span>
        </span>
        {/* Clipped from the LEFT. CSS truncate cut the END of every path,
            so three edits to three different files in one package all
            rendered as the identical "internal/handle…". Shell details are
            shortened the way the rest of the UI shortens a home
            directory. */}
        <span
          role="cell"
          className="truncate px-3 py-1.5 font-mono text-xs text-ink-dim"
          title={t.detail}
        >
          {hasDiff && (
            <span className="mr-1.5 text-accent">{open ? "▾" : "▸"} diff</span>
          )}
          {t.detail &&
            (t.kind === "shell"
              ? clipCommand(t.detail, 72)
              : clipPath(t.detail, 56))}
        </span>
        <span role="cell" className="px-3 py-1.5 text-right">
          <JumpButton seq={t.messageSeq} onJump={onJump} />
        </span>
        <span
          role="cell"
          title={fullWhen(t.at ?? "")}
          className="px-3 py-1.5 text-right font-mono text-meta text-ink-faint tabular-nums"
        >
          {localClock(t.at ?? "")}
        </span>
      </div>
      {hasDiff && open && (
        <div className="px-3 py-2">
          <ToolExpansion agent={agent} sessionId={sessionId} seq={t.seq} />
        </div>
      )}
    </div>
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
  // Keyed by path, and owned by the list — see ToolsTab.
  const [open, toggle] = useToggleSet<string>();
  if (files.length === 0)
    return <EmptyNote>No files touched in this session.</EmptyNote>;
  return (
    <div className="overflow-hidden rounded-md border border-edge">
      <WindowedList
        items={files}
        getKey={(f) => f.path}
        estimateSize={32}
        className="bg-surface-1"
      >
        {(f) => (
          <FileRow
            agent={agent}
            sessionId={sessionId}
            f={f}
            open={open.has(f.path)}
            onToggle={() => toggle(f.path)}
            onJump={onJump}
          />
        )}
      </WindowedList>
    </div>
  );
}

function FileRow({
  agent,
  sessionId,
  f,
  open,
  onToggle,
  onJump,
}: {
  agent: string;
  sessionId: string;
  f: FileGroup;
  open: boolean;
  onToggle: () => void;
  onJump: (seq: number) => void;
}) {
  const diffs = f.changes;
  const edits = countKind(f, "file_edit");
  const writes = countKind(f, "file_write");
  const summary = (
    <>
      {diffs.length > 0 && (
        <span className="shrink-0 font-mono text-meta text-accent">
          {open ? "▾" : "▸"}
        </span>
      )}
      <span
        className="min-w-0 flex-1 truncate font-mono text-xs"
        title={f.path}
      >
        {shortPath(f.path)}
      </span>
      <span className="ml-auto flex shrink-0 gap-2 font-mono text-micro text-ink-faint tabular-nums">
        {edits > 0 && (
          <span className="text-warn">{plural(edits, "edit")}</span>
        )}
        {writes > 0 && (
          <span className="text-ok">{plural(writes, "write")}</span>
        )}
        {f.reads > 0 && <span>{plural(f.reads, "read")}</span>}
      </span>
    </>
  );
  return (
    <div className="border-b border-edge bg-surface-1">
      {/* When there are diffs to reveal, the summary IS the control — a
          real button, so it is reachable by tab and announces its state.
          The copy control stays outside it: a button inside a button is
          neither valid nor operable. */}
      <div className="flex items-baseline gap-3 pr-3">
        {diffs.length > 0 ? (
          <button
            type="button"
            onClick={onToggle}
            aria-expanded={open}
            className="flex min-w-0 flex-1 items-baseline gap-3 py-1.5 pl-3 text-left transition-colors hover:bg-surface-2/40"
          >
            {summary}
          </button>
        ) : (
          <div className="flex min-w-0 flex-1 items-baseline gap-3 py-1.5 pl-3">
            {summary}
          </div>
        )}
        <CopyButton text={f.path} />
      </div>
      {open && (
        <div className="space-y-2 border-t border-edge px-3 py-2">
          {diffs.map((e) => (
            <div key={e.seq}>
              <div className="mb-1 flex items-center gap-2 font-mono text-micro text-ink-faint tabular-nums">
                <span title={fullWhen(e.at ?? "")}>
                  {e.kind === "file_write" ? "write" : "edit"} #{e.seq} ·{" "}
                  {localClock(e.at ?? "")}
                </span>
                <JumpButton seq={e.messageSeq} onJump={onJump} />
              </div>
              <ToolExpansion agent={agent} sessionId={sessionId} seq={e.seq} />
            </div>
          ))}
        </div>
      )}
    </div>
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
            <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-micro text-accent">
              {kindLabel(a.kind)}
            </span>
            <span className="truncate font-mono text-xs">{a.name}</span>
            <span className="ml-auto shrink-0 font-mono text-micro text-ink-faint">
              {a.relation} · {a.evidence}
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
