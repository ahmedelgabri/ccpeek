import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Link,
  useNavigate,
  useParams,
  useSearch,
} from "@tanstack/react-router";
import { api, fmtCost, fmtTokens, shortPath } from "../api";
import {
  AgentChip,
  SkeletonRows,
  SkeletonTiles,
  StatTile,
  TokenMixBar,
} from "../ui";
import { Transcript } from "./session/Transcript";
import {
  ArtifactsTab,
  CommandsTab,
  FilesTab,
  ToolsTab,
  groupFiles,
} from "./session/tabs";
import { useSessionTools, useTranscriptWindow } from "./session/useSessionData";

const TABS = ["transcript", "commands", "tools", "files", "artifacts"] as const;

// The gathering point of the session-centric model: one session with its
// transcript, commands, tool calls, files touched, usage, relations, and
// linked artifacts — each facet a deep-linkable tab (?tab=). The paging
// and anchoring machinery all lives in ./session/useSessionData.
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
  const win = useTranscriptWindow(agent, sessionId, search.seq);
  const { rows: toolRows, loading: toolsLoading } = useSessionTools(
    agent,
    sessionId,
    tab === "commands" || tab === "tools" || tab === "files",
  );
  // Derived above the early returns so they can be memoized. useSessionTools
  // latches once any tool tab has been opened, so without this both ran over
  // the session's entire tool set on every render of this page — including
  // the renders scroll-spy triggers while the transcript tab is active.
  const commands = useMemo(
    () => toolRows.filter((t) => t.kind === "shell" && t.detail),
    [toolRows],
  );
  const files = useMemo(() => groupFiles(toolRows), [toolRows]);

  if (detail.isLoading)
    return (
      <div className="space-y-4">
        <SkeletonTiles />
        <SkeletonRows rows={6} />
      </div>
    );
  if (detail.error) return <p className="text-warn">{String(detail.error)}</p>;
  const s = detail.data!;
  // Badge counts for the tab bar; transcript carries none.
  const counts: Record<string, number | null> = {
    transcript: null,
    commands: commands.length,
    tools: toolRows.length,
    files: files.length,
    artifacts: s.artifacts?.length ?? 0,
  };

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

      {s.tokens.input +
        s.tokens.output +
        s.tokens.cacheRead +
        s.tokens.cacheWrite >
        0 && (
        <div className="mb-4 rounded-md border border-edge bg-surface-1 px-3 py-2.5">
          <div className="microlabel mb-2">Token mix</div>
          <TokenMixBar tokens={s.tokens} fmt={fmtTokens} />
        </div>
      )}

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
          const count = counts[t];
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
          key={sessionId}
          agent={agent}
          sessionId={sessionId}
          total={s.messages}
          transcript={win}
        />
      )}
      {tab === "commands" &&
        (toolsLoading ? (
          <SkeletonRows rows={4} />
        ) : (
          <CommandsTab commands={commands} onJump={win.jumpToSeq} />
        ))}
      {tab === "tools" &&
        (toolsLoading ? (
          <SkeletonRows rows={4} />
        ) : (
          <ToolsTab
            agent={agent}
            sessionId={sessionId}
            tools={toolRows}
            onJump={win.jumpToSeq}
          />
        ))}
      {tab === "files" &&
        (toolsLoading ? (
          <SkeletonRows rows={4} />
        ) : (
          <FilesTab
            agent={agent}
            sessionId={sessionId}
            files={files}
            onJump={win.jumpToSeq}
          />
        ))}
      {tab === "artifacts" && (
        <ArtifactsTab agent={s.agent} artifacts={s.artifacts ?? []} />
      )}
    </div>
  );
}
