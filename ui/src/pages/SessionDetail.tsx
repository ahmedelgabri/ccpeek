import { useMemo, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Link,
  useNavigate,
  useParams,
  useSearch,
} from "@tanstack/react-router";
import { api, fmtCount, fmtTokens, shortPath } from "../api";
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
import {
  ArtifactsTab,
  CommandsTab,
  FilesTab,
  ToolsTab,
  groupFiles,
} from "./session/tabs";
import { useSessionTools, useTranscriptWindow } from "./session/useSessionData";

const TABS = ["transcript", "commands", "tools", "files", "artifacts"] as const;
type Tab = (typeof TABS)[number];

// A ?tab= from the URL is any string until proven otherwise.
const isTab = (v: string | undefined): v is Tab => TABS.some((t) => t === v);

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

      {/* Token facets that are zero are dropped rather than tiled. Most
          sessions report no cache or no output, and six tiles of which
          four read "0" is a row of chrome saying nothing — the figures
          that DO exist get the space instead. */}
      <div className="mb-4 grid grid-cols-2 gap-3 sm:grid-cols-4 xl:grid-cols-6">
        <StatTile
          label="Cost"
          value={<Money usd={s.costUSD} unpriced={s.unpricedTokens} />}
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
          options={TABS.map((t) => ({
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
    </div>
  );
}
