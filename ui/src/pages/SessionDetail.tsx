import { useMemo, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Link,
  useNavigate,
  useParams,
  useSearch,
} from "@tanstack/react-router";
import {
  api,
  fmtCount,
  fmtTokens,
  plural,
  shortPath,
  type Relation,
} from "../api";
import { ErrorPanel } from "../ErrorState";
import {
  AgentChip,
  LoadError,
  Loading,
  Money,
  Panel,
  Segmented,
  SkeletonRows,
  SkeletonTiles,
  StatTile,
  TokenMixBar,
} from "../ui";
import { Transcript } from "./session/Transcript";
import { ContextTab } from "./session/ContextChart";
import { EventList, groupPRs, PRLink } from "./insights/events";
import {
  ArtifactsTab,
  CommandsTab,
  FilesTab,
  SubagentsTab,
  ToolsTab,
  groupFiles,
} from "./session/tabs";
import { useSessionTools, useTranscriptWindow } from "./session/useSessionData";

const TABS = [
  "transcript",
  "commands",
  "tools",
  "files",
  "artifacts",
  "subagents",
  "events",
  "context",
] as const;
type Tab = (typeof TABS)[number];

// A ?tab= from the URL is any string until proven otherwise.
const isTab = (v: string | undefined): v is Tab => TABS.some((t) => t === v);

// The gathering point of the session-centric model: one session with its
// transcript, commands, tool calls, files touched, usage, relations, and
// linked artifacts — each facet a deep-linkable tab (?tab=). The paging
// and anchoring machinery all lives in ./session/useSessionData.
// SessionPRs shows the pull requests this session opened or checked.
function SessionPRs({
  agent,
  sessionId,
}: {
  agent: string;
  sessionId: string;
}) {
  const q = useQuery({
    queryKey: ["sessionEvents", agent, sessionId, "pr_link"],
    queryFn: () =>
      api.sessionEvents({ agent, session: sessionId, kind: "pr_link" }),
  });
  const prs = groupPRs(q.data ?? []);
  if (prs.length === 0) return null;
  return (
    <div className="mb-2 flex flex-wrap items-baseline gap-2 text-xs">
      <span className="font-mono text-micro text-ink-faint">pull requests</span>
      {prs.map((pr) => (
        <span
          key={pr.url}
          className="rounded border border-edge px-2 py-0.5 font-mono"
        >
          <PRLink pr={pr} />
        </span>
      ))}
    </div>
  );
}

// SessionEvents lists the session's events, fetched when the tab opens.
function SessionEvents({
  agent,
  sessionId,
  onJump,
}: {
  agent: string;
  sessionId: string;
  onJump: (seq: number) => void;
}) {
  const q = useQuery({
    queryKey: ["sessionEvents", agent, sessionId],
    queryFn: () =>
      api.sessionEvents({ agent, session: sessionId, limit: 1000 }),
  });
  if (q.isLoading) return <SkeletonRows rows={4} />;
  if (q.error) return <LoadError error={q.error} />;
  return (
    <div className="overflow-hidden rounded-md border border-edge">
      <EventList
        events={q.data ?? []}
        showSession={false}
        onJump={onJump}
        empty="No events recorded for this session."
      />
    </div>
  );
}

// relationLabel reads an edge from this session's side.
function relationLabel(r: Relation, subagentType?: string): string {
  const out = r.direction === "out";
  switch (r.kind) {
    case "subagent_of":
      return `${subagentType || "subagent"} of`;
    case "resumed_from":
      return out ? "resumed from" : "continued in";
    case "fork_of":
      return out ? "fork of" : "forked into";
    default:
      return out ? r.kind : `${r.kind} (incoming)`;
  }
}

export function SessionDetailPage() {
  const { agent, sessionId } = useParams({
    from: "/sessions/$agent/$sessionId",
  });
  return <SessionContent key={`${agent}/${sessionId}`} />;
}

function SessionContent() {
  const { agent, sessionId } = useParams({
    from: "/sessions/$agent/$sessionId",
  });
  const search = useSearch({ from: "/sessions/$agent/$sessionId" });
  const navigate = useNavigate({ from: "/sessions/$agent/$sessionId" });
  const tab: Tab = isTab(search.tab) ? search.tab : "transcript";

  const detail = useQuery({
    queryKey: ["session", agent, sessionId],
    queryFn: () => api.session(agent, sessionId),
  });
  const win = useTranscriptWindow(agent, sessionId, search.seq);
  const {
    rows: toolRows,
    loading: toolsLoading,
    error: toolsError,
    requested: toolsRequested,
  } = useSessionTools(
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
      <Loading label="Loading session…">
        <SkeletonTiles />
        <SkeletonRows rows={6} />
      </Loading>
    );
  // A failed session load used to render one bare orange line at the top
  // of an otherwise blank page — no styling, no way back — while the
  // shared ErrorPanel sat unused. A 404 here is ordinary (a stale
  // permalink, a re-indexed session), so it says so and offers the way out.
  if (detail.error)
    return (
      <div className="space-y-3">
        <ErrorPanel error={detail.error} scope="this session" />
        <Link
          to="/sessions"
          className="inline-block font-mono text-xs text-accent hover:underline"
        >
          ← back to all sessions
        </Link>
      </div>
    );
  const s = detail.data!;
  const eventCount = Object.values(s.events ?? {}).reduce((a, b) => a + b, 0);
  // Badge counts for the tab bar; transcript carries none. The
  // tool-derived counts are withheld until the rows have actually loaded —
  // they load lazily, so rendering the not-yet-known value would label a
  // session with eight tool calls "tools 0".
  const toolCountsKnown = toolsRequested && !toolsLoading && !toolsError;
  // The three tool-fed tabs share one gate: skeleton while the rows load,
  // the failure when there was one, the tab itself otherwise. Rows that
  // did arrive before the failure are still shown — but their tab's
  // "nothing here" note is not, because that is not what happened.
  const toolPanel = (body: ReactNode) => {
    if (toolsLoading) return <SkeletonRows rows={4} />;
    if (toolsError == null) return body;
    return (
      <div className="space-y-3">
        <LoadError error={toolsError} />
        {toolRows.length > 0 && body}
      </div>
    );
  };
  const counts: Record<Tab, number | null> = {
    transcript: null,
    commands: toolCountsKnown ? commands.length : null,
    tools: toolCountsKnown ? toolRows.length : null,
    files: toolCountsKnown ? files.length : null,
    artifacts: s.artifacts?.length ?? 0,
    subagents: s.subagentCount ?? 0,
    events: eventCount,
    context: null,
  };
  // Most sessions spawn nothing and record no events; those tabs appear
  // only when there is something in them.
  const tabs = TABS.filter(
    (t) =>
      (t !== "subagents" || (s.subagentCount ?? 0) > 0) &&
      (t !== "events" || eventCount > 0) &&
      (t !== "context" || s.tokens.input + s.tokens.cacheRead > 0),
  );
  // The subagent_of edges are the subagents tab; only the outgoing one (a
  // subagent naming its parent) belongs beside the models.
  const relations = s.relations?.filter(
    (r) => r.kind !== "subagent_of" || r.direction === "out",
  );

  return (
    <div>
      <div className="mb-1 flex items-baseline gap-3">
        <AgentChip agent={s.agent} />
        <h1 className="min-w-0 flex-1 truncate text-xl font-semibold">
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

      {/* Token facets that are zero are dropped rather than tiled. Most
          sessions report no cache or no output, and six tiles of which
          four read "0" is a row of chrome saying nothing — the figures
          that DO exist get the space instead. */}
      <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4 xl:grid-cols-6">
        <StatTile
          label="Cost"
          value={
            <Money
              usd={s.costUSD}
              incomplete={(s.unpricedTokens ?? 0) > 0}
              title={`$${s.costUSDExact}`}
            />
          }
        />
        {s.tokens.input > 0 && (
          <StatTile label="Input" value={fmtTokens(s.tokens.input)} />
        )}
        {s.tokens.output > 0 && (
          <StatTile label="Output" value={fmtTokens(s.tokens.output)} />
        )}
        {s.tokens.cacheRead > 0 && (
          <StatTile label="Cache read" value={fmtTokens(s.tokens.cacheRead)} />
        )}
        <StatTile label="Messages" value={fmtCount(s.messages)} />
        <StatTile label="Tool calls" value={fmtCount(s.toolCalls)} />
        {s.lines && (
          <StatTile
            label="Lines changed"
            value={
              <span className="tabular-nums">
                <span className="text-ok">+{fmtCount(s.lines.added)}</span>{" "}
                <span className="text-warn">−{fmtCount(s.lines.removed)}</span>
              </span>
            }
            detail={plural(s.lines.files, "file")}
          />
        )}
        {s.subagentTotal && (
          <StatTile
            label="Subagent cost"
            value={
              <Money
                usd={s.subagentTotal.costUSD}
                incomplete={(s.subagentTotal.unpricedTokens ?? 0) > 0}
                title={`$${s.subagentTotal.costUSDExact} across ${s.subagentTotal.sessions} subagent sessions`}
              />
            }
          />
        )}
      </div>

      {s.tokens.input +
        s.tokens.output +
        s.tokens.cacheRead +
        s.tokens.cacheWrite >
        0 && (
        <Panel label="Token mix" className="mb-4">
          <div className="px-3 py-2.5">
            <TokenMixBar tokens={s.tokens} fmt={fmtTokens} />
          </div>
        </Panel>
      )}

      {s.unpricedTokens ? (
        <p className="mb-4 rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
          {fmtTokens(s.unpricedTokens)} tokens have no resolvable model or
          bucket rate — the API-equivalent cost shown is a lower bound.
        </p>
      ) : null}

      {(s.events?.pr_link ?? 0) > 0 && (
        <SessionPRs agent={agent} sessionId={sessionId} />
      )}

      {(relations?.length || s.models?.length) && (
        <div className="mb-4 flex flex-wrap gap-2 text-xs">
          {s.models?.map((m) => (
            <span
              key={m}
              className="rounded border border-edge px-2 py-0.5 font-mono text-ink-dim"
            >
              {m}
            </span>
          ))}
          {relations?.map((r) => (
            <Link
              key={`${r.kind}-${r.direction}-${r.sessionId}`}
              to="/sessions/$agent/$sessionId"
              params={{ agent: s.agent, sessionId: r.sessionId }}
              // atSeq is a seq in the PARENT: the link opens the parent at
              // the branch point; from the parent's side, at the child's
              // start.
              search={
                r.direction === "out" && r.atSeq !== undefined
                  ? { seq: r.atSeq }
                  : {}
              }
              title={
                r.atSeq !== undefined
                  ? `Branched after message #${r.atSeq} of the parent`
                  : undefined
              }
              className="rounded border border-accent/40 px-2 py-0.5 font-mono text-accent hover:bg-surface-2"
            >
              {relationLabel(r, s.subagentType)} → {r.sessionId.slice(0, 8)}
              {r.atSeq !== undefined && ` @#${r.atSeq}`}
            </Link>
          ))}
        </div>
      )}

      <div className="mb-3">
        <Segmented
          label="Session facet"
          variant="tab"
          value={tab}
          // ?seq travels with the tab. Replacing the whole search object
          // dropped it, so opening the files tab from a permalink and
          // coming back landed at the top of the session — the message the
          // link was FOR was gone from the URL.
          onChange={(t) =>
            void navigate({
              search: (prev: { tab?: string; seq?: number }) => ({
                ...prev,
                tab: t === "transcript" ? undefined : t,
              }),
              replace: true,
            })
          }
          options={tabs.map((t) => ({
            value: t,
            label: t,
            badge: counts[t] === null ? undefined : fmtCount(counts[t]),
          }))}
        />
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
      {/* Loading, failed, and empty are three different answers. A failed
          tool fetch used to reach the tabs as an empty row set, which they
          reported as "No tool calls recorded" — the archive blamed for the
          request. */}
      {tab === "commands" &&
        toolPanel(<CommandsTab commands={commands} onJump={win.jumpToSeq} />)}
      {tab === "tools" &&
        toolPanel(
          <ToolsTab
            agent={agent}
            sessionId={sessionId}
            tools={toolRows}
            onJump={win.jumpToSeq}
          />,
        )}
      {tab === "files" &&
        toolPanel(
          <FilesTab
            agent={agent}
            sessionId={sessionId}
            files={files}
            onJump={win.jumpToSeq}
          />,
        )}
      {tab === "artifacts" && (
        <ArtifactsTab agent={s.agent} artifacts={s.artifacts ?? []} />
      )}
      {tab === "subagents" && (
        <SubagentsTab subagents={s.subagents ?? []} total={s.subagentTotal} />
      )}
      {tab === "context" && (
        <ContextTab
          agent={agent}
          sessionId={sessionId}
          onJump={win.jumpToSeq}
        />
      )}
      {tab === "events" && (
        <SessionEvents
          agent={agent}
          sessionId={sessionId}
          onJump={win.jumpToSeq}
        />
      )}
    </div>
  );
}
