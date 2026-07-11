import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useHighlight } from "../highlight";
import { DiffView } from "../Diff";
import {
  api,
  fmtCost,
  fmtTokens,
  shortPath,
  type ToolCallRow,
  type TranscriptMessage,
} from "../api";
import {
  AgentChip,
  CopyButton,
  EmptyNote,
  KindBars,
  SkeletonRows,
  SkeletonTiles,
  StatTile,
  TokenMixBar,
} from "../ui";

const TABS = ["transcript", "commands", "tools", "files", "artifacts"] as const;

// The gathering point of the session-centric model: one session with its
// transcript, commands, tool calls, files touched, usage, relations, and
// linked artifacts — each facet a deep-linkable tab (?tab=).
export function SessionDetailPage() {
  const { agent, sessionId } = useParams({
    from: "/sessions/$agent/$sessionId",
  });
  const search = useSearch({ from: "/sessions/$agent/$sessionId" });
  const navigate = useNavigate({ from: "/sessions/$agent/$sessionId" });
  const tab =
    search.tab && (TABS as readonly string[]).includes(search.tab)
      ? search.tab
      : "transcript";

  const detail = useQuery({
    queryKey: ["session", agent, sessionId],
    queryFn: () => api.session(agent, sessionId),
  });
  const transcript = useQuery({
    queryKey: ["transcript", agent, sessionId],
    queryFn: () => api.transcript(agent, sessionId, { limit: "1000" }),
  });
  const tools = useQuery({
    queryKey: ["tools", agent, sessionId],
    queryFn: () => api.sessionTools(agent, sessionId),
  });

  if (detail.isLoading)
    return (
      <div className="space-y-4">
        <SkeletonTiles />
        <SkeletonRows rows={6} />
      </div>
    );
  if (detail.error) return <p className="text-warn">{String(detail.error)}</p>;
  const s = detail.data!;
  const toolRows = tools.data ?? [];
  const commands = toolRows.filter((t) => t.kind === "shell" && t.detail);
  const files = groupFiles(toolRows);

  return (
    <div>
      <div className="mb-1 flex items-baseline gap-3">
        <AgentChip agent={s.agent} />
        <h1 className="truncate text-xl font-semibold">
          {s.title || "(untitled)"}
        </h1>
      </div>
      <p className="mb-4 font-mono text-xs text-ink-faint">
        {s.cwd && (
          <Link
            to="/sessions"
            search={{ project: s.cwd }}
            className="hover:text-accent"
          >
            {shortPath(s.cwd)}
          </Link>
        )}
        {s.gitBranch && <> · ⎇ {s.gitBranch}</>} · {s.id}
      </p>

      <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4 xl:grid-cols-6">
        <StatTile
          label="Cost"
          value={fmtCost(s.costUSD, s.unpricedTokens)}
          tone="ok"
        />
        <StatTile label="Input" value={fmtTokens(s.tokens.input)} />
        <StatTile label="Output" value={fmtTokens(s.tokens.output)} />
        <StatTile label="Cache read" value={fmtTokens(s.tokens.cacheRead)} />
        <StatTile label="Messages" value={String(s.messages)} />
        <StatTile label="Tool calls" value={String(s.toolCalls)} />
      </div>

      <div className="mb-4 rounded-md border border-edge bg-surface-1 px-3 py-2.5">
        <div className="microlabel mb-2">Token mix</div>
        <TokenMixBar tokens={s.tokens} fmt={fmtTokens} />
      </div>

      {s.unpricedTokens ? (
        <p className="mb-4 rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
          {fmtTokens(s.unpricedTokens)} tokens use a model the pricing table
          can't resolve — the cost shown is a lower bound.
        </p>
      ) : null}

      {(s.relations?.length || s.models?.length) && (
        <div className="mb-4 flex flex-wrap gap-2 text-xs">
          {s.models?.map((m) => (
            <span
              key={m}
              className="rounded border border-edge px-2 py-0.5 font-mono text-ink-dim"
            >
              {m}
            </span>
          ))}
          {s.relations?.map((r) => (
            <Link
              key={`${r.kind}-${r.sessionId}`}
              to="/sessions/$agent/$sessionId"
              params={{ agent: s.agent, sessionId: r.sessionId }}
              className="rounded border border-accent/40 px-2 py-0.5 font-mono text-accent hover:bg-surface-2"
            >
              {r.direction === "out" ? r.kind : `${r.kind} (incoming)`} →{" "}
              {r.sessionId.slice(0, 8)}
            </Link>
          ))}
        </div>
      )}

      <div className="mb-3 flex rounded-md border border-edge font-mono text-xs">
        {TABS.map((t) => {
          const count =
            t === "commands"
              ? commands.length
              : t === "tools"
                ? toolRows.length
                : t === "files"
                  ? files.length
                  : t === "artifacts"
                    ? (s.artifacts?.length ?? 0)
                    : null;
          return (
            <button
              key={t}
              onClick={() =>
                void navigate({
                  search: t === "transcript" ? {} : { tab: t },
                  replace: true,
                })
              }
              className={`px-3 py-1.5 first:rounded-l-md last:rounded-r-md ${
                t === tab
                  ? "bg-surface-2 text-ink"
                  : "text-ink-dim hover:text-ink"
              }`}
            >
              {t}
              {count !== null && count > 0 && (
                <span className="ml-1.5 text-ink-faint tabular-nums">
                  {count}
                </span>
              )}
            </button>
          );
        })}
      </div>

      {tab === "transcript" && (
        <Transcript
          msgs={transcript.data ?? []}
          tools={toolRows}
          focusSeq={search.seq}
        />
      )}
      {tab === "commands" && <CommandsTab commands={commands} />}
      {tab === "tools" && <ToolsTab tools={toolRows} />}
      {tab === "files" && <FilesTab files={files} />}
      {tab === "artifacts" && (
        <ArtifactsTab
          agent={s.agent}
          artifacts={s.artifacts ?? []}
        />
      )}
    </div>
  );
}

function Transcript({
  msgs,
  tools,
  focusSeq,
}: {
  msgs: TranscriptMessage[];
  tools: ToolCallRow[];
  focusSeq?: number;
}) {
  const [treeView, setTreeView] = useState(false);
  const container = useRef<HTMLDivElement>(null);
  useHighlight(container, [msgs]);
  const depths = useMemo(() => computeDepths(msgs), [msgs]);
  const toolsByMsg = useMemo(() => {
    const map = new Map<number, ToolCallRow[]>();
    for (const t of tools) {
      const list = map.get(t.messageSeq) ?? [];
      list.push(t);
      map.set(t.messageSeq, list);
    }
    return map;
  }, [tools]);

  // Tool-only messages carry no prose: fold them into their tool chips
  // and drop rows that have neither text nor tools (unless deep-linked).
  const visible = msgs.filter(
    (m) =>
      m.text.trim() !== "" ||
      (toolsByMsg.get(m.seq)?.length ?? 0) > 0 ||
      m.seq === focusSeq,
  );
  const hidden = msgs.length - visible.length;

  // Search hits deep-link to ?seq=N: scroll it into view once loaded.
  useEffect(() => {
    if (focusSeq === undefined || msgs.length === 0) return;
    document
      .getElementById(`seq-${focusSeq}`)
      ?.scrollIntoView({ block: "center" });
  }, [focusSeq, msgs.length]);

  return (
    <div ref={container}>
      <div className="mb-2 flex items-center gap-3">
        {hidden > 0 && (
          <span className="font-mono text-[11px] text-ink-faint">
            {hidden} empty entries folded
          </span>
        )}
        <button
          onClick={() => setTreeView((v) => !v)}
          className={`ml-auto rounded-md border border-edge px-2 py-1 font-mono text-xs ${
            treeView ? "bg-surface-2 text-ink" : "text-ink-dim hover:text-ink"
          }`}
        >
          tree view
        </button>
      </div>
      <ol className="space-y-2">
        {visible.map((m) => {
          const msgTools = toolsByMsg.get(m.seq) ?? [];
          // Three visual registers: the user's prompts (accent rule,
          // raised surface), the assistant's replies (quiet card), and
          // meta entries — system events, summaries, tool-only rows —
          // (compact, dimmed, dashed).
          const isUser = m.role === "user" && m.kind === "message";
          const isAssistant = m.role === "assistant";
          const isMeta = !isUser && !isAssistant;
          const glyph = isUser ? "❯" : isAssistant ? "✦" : "·";
          return (
            <li
              key={m.seq}
              id={`seq-${m.seq}`}
              style={
                treeView
                  ? {
                      marginLeft: `${Math.min(depths.get(m.seq) ?? 0, 12) * 16}px`,
                    }
                  : undefined
              }
              className={`rounded-md border border-l-2 ${
                m.seq === focusSeq
                  ? "border-accent"
                  : isUser
                    ? "border-edge border-l-accent"
                    : isMeta
                      ? "border-edge border-dashed"
                      : "border-edge border-l-edge-strong"
              } ${
                isUser
                  ? "bg-surface-2/60 p-3"
                  : isMeta
                    ? "bg-transparent px-3 py-1.5"
                    : "bg-surface-1 p-3"
              } ${m.isSidechain && !treeView ? "ml-8 border-dashed" : ""} ${
                m.isSidechain && treeView ? "border-dashed" : ""
              }`}
            >
              <div
                className={`flex gap-2 font-mono text-[11px] text-ink-faint ${isMeta ? "" : "mb-1"}`}
              >
                <span
                  className={
                    isAssistant
                      ? "text-accent"
                      : isUser
                        ? "font-medium text-ink"
                        : ""
                  }
                >
                  {m.isSidechain ? "↳ " : ""}
                  {glyph} {m.role}
                  {m.kind !== "message" ? ` · ${m.kind}` : ""}
                </span>
                {m.model && <span>{m.model}</span>}
                {isMeta && m.text.trim() !== "" && (
                  <span className="truncate text-ink-faint italic">
                    {m.text.slice(0, 120)}
                  </span>
                )}
                <span className="ml-auto tabular-nums">
                  #{m.seq} · {m.createdAt.slice(11, 19)}
                </span>
              </div>
              {!isMeta &&
                m.text.trim() !== "" &&
                (m.html ? (
                  <div
                    className="prose-msg"
                    dangerouslySetInnerHTML={{ __html: m.html }}
                  />
                ) : (
                  <div className="text-sm leading-relaxed whitespace-pre-wrap">
                    {m.text}
                  </div>
                ))}
              {msgTools.length > 0 && (
                <div
                  className={`flex flex-wrap gap-1.5 ${m.text.trim() !== "" ? "mt-2" : ""}`}
                >
                  {msgTools.map((t) => (
                    <span
                      key={t.seq}
                      title={t.detail}
                      className="inline-flex max-w-full items-baseline gap-1.5 rounded border border-edge bg-surface-2/60 px-1.5 py-0.5 font-mono text-[11px]"
                    >
                      <span className="text-accent">{t.name}</span>
                      {t.detail && (
                        <span className="truncate text-ink-dim">
                          {t.kind === "shell"
                            ? t.detail.split("\n")[0].slice(0, 80)
                            : shortPath(t.detail)}
                        </span>
                      )}
                    </span>
                  ))}
                </div>
              )}
            </li>
          );
        })}
      </ol>
      {msgs.length === 0 && <EmptyNote>No transcript entries.</EmptyNote>}
    </div>
  );
}

function CommandsTab({ commands }: { commands: ToolCallRow[] }) {
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

function ToolsTab({ tools }: { tools: ToolCallRow[] }) {
  if (tools.length === 0) return <EmptyNote>No tool calls recorded.</EmptyNote>;
  const byKind = new Map<string, number>();
  for (const t of tools) byKind.set(t.kind, (byKind.get(t.kind) ?? 0) + 1);
  const kinds = Array.from(byKind, ([label, count]) => ({ label, count })).sort(
    (a, b) => b.count - a.count,
  );
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
              <th className="px-3 py-1.5 text-right">at</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-edge bg-surface-1">
            {tools.map((t) => (
              <ToolRow key={t.seq} t={t} />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ToolRow({ t }: { t: ToolCallRow }) {
  const [open, setOpen] = useState(false);
  const hasDiff = t.kind === "file_edit" && (t.old || t.new);
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
          <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink-dim">
            {t.kind}
          </span>
        </td>
        <td className="max-w-0 truncate px-3 py-1.5 font-mono text-xs text-ink-dim">
          {hasDiff && (
            <span className="mr-1.5 text-accent">{open ? "▾" : "▸"} diff</span>
          )}
          {t.detail && shortPath(t.detail)}
        </td>
        <td className="px-3 py-1.5 text-right font-mono text-[11px] text-ink-faint tabular-nums">
          {t.at?.slice(11, 19)}
        </td>
      </tr>
      {hasDiff && open && (
        <tr>
          <td colSpan={5} className="px-3 py-2">
            <DiffView old={t.old ?? ""} new={t.new ?? ""} />
          </td>
        </tr>
      )}
    </>
  );
}

interface FileGroup {
  path: string;
  reads: number;
  writes: number;
  edits: ToolCallRow[];
}

function groupFiles(tools: ToolCallRow[]): FileGroup[] {
  const map = new Map<string, FileGroup>();
  for (const t of tools) {
    if (!t.detail || !t.kind.startsWith("file_")) continue;
    const g = map.get(t.detail) ?? {
      path: t.detail,
      reads: 0,
      writes: 0,
      edits: [],
    };
    if (t.kind === "file_read") g.reads++;
    if (t.kind === "file_write") g.writes++;
    if (t.kind === "file_edit") g.edits.push(t);
    map.set(t.detail, g);
  }
  return Array.from(map.values()).sort(
    (a, b) => b.writes + b.edits.length - (a.writes + a.edits.length),
  );
}

function FilesTab({ files }: { files: FileGroup[] }) {
  if (files.length === 0)
    return <EmptyNote>No files touched in this session.</EmptyNote>;
  return (
    <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
      {files.map((f) => (
        <FileRow key={f.path} f={f} />
      ))}
    </ul>
  );
}

function FileRow({ f }: { f: FileGroup }) {
  const [open, setOpen] = useState(false);
  const diffs = f.edits.filter((e) => e.old || e.new);
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
          {f.edits.length > 0 && (
            <span className="text-warn">{f.edits.length} edits</span>
          )}
          {f.writes > 0 && <span className="text-ok">{f.writes} writes</span>}
          {f.reads > 0 && <span>{f.reads} reads</span>}
        </span>
        <CopyButton text={f.path} />
      </div>
      {open && (
        <div className="space-y-2 border-t border-edge px-3 py-2">
          {diffs.map((e) => (
            <div key={e.seq}>
              <div className="mb-1 font-mono text-[10px] text-ink-faint tabular-nums">
                edit #{e.seq} · {e.at?.slice(11, 19)}
              </div>
              <DiffView old={e.old ?? ""} new={e.new ?? ""} />
            </div>
          ))}
        </div>
      )}
    </li>
  );
}

function ArtifactsTab({
  agent,
  artifacts,
}: {
  agent: string;
  artifacts: { kind: string; name: string; relation: string; evidence: string }[];
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

// computeDepths walks the agent-native entry tree (Claude parentUuid, Pi
// id/parentId): an entry's depth is parent depth + 1 when the parent is
// NOT the immediately preceding entry (a real branch), else parent depth —
// keeping the main thread flat while branches indent.
function computeDepths(msgs: TranscriptMessage[]): Map<number, number> {
  const bySeq = new Map<number, number>();
  const byExternal = new Map<string, TranscriptMessage>();
  for (const m of msgs) {
    if (m.externalId) byExternal.set(m.externalId, m);
  }
  let prev: TranscriptMessage | undefined;
  for (const m of msgs) {
    let depth = 0;
    const parent = m.parentId ? byExternal.get(m.parentId) : undefined;
    if (parent) {
      const parentDepth = bySeq.get(parent.seq) ?? 0;
      depth = prev && parent.seq !== prev.seq ? parentDepth + 1 : parentDepth;
    } else if (m.isSidechain) {
      depth = 1;
    }
    bySeq.set(m.seq, depth);
    prev = m;
  }
  return bySeq;
}
