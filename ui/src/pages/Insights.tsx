import { useQuery } from "@tanstack/react-query";
import { useSearch } from "@tanstack/react-router";
import {
  api,
  fmtCount,
  fmtTokens,
  shortPath,
  type InsightFilters,
} from "../api";
import {
  AgentChip,
  EmptyNote,
  FilterBar,
  KindBars,
  LoadError,
  PageHeader,
  Panel,
  Segmented,
  SkeletonRows,
  StatTile,
  useSetFilter,
} from "../ui";
import { Link } from "@tanstack/react-router";
import { fmtDuration, fmtWhen, fullWhen } from "../time";
import { EventList, groupPRs, PRLink } from "./insights/events";
import { EffortTab } from "./insights/effort";

const TABS = [
  "errors",
  "prs",
  "timing",
  "compaction",
  "hooks",
  "changes",
  "effort",
] as const;
type Tab = (typeof TABS)[number];
const isTab = (v: string | undefined): v is Tab => TABS.some((t) => t === v);

// Insights reads across sessions for what went on around the
// conversations: what failed, what was refused, and (as more kinds land)
// how long things took. Filters live in the URL like every data view.
export function InsightsPage() {
  const search = useSearch({ from: "/insights" });
  const setFilter = useSetFilter("/insights");
  const tab: Tab = isTab(search.tab) ? search.tab : TABS[0];
  const f: InsightFilters = {
    agent: search.agent ?? "",
    since: search.since ?? "",
    until: search.until ?? "",
  };
  return (
    <div>
      <PageHeader title="Insights">
        <FilterBar
          since={f.since}
          until={f.until}
          onRange={(sv, uv) => setFilter({ since: sv, until: uv })}
          agent={f.agent ?? ""}
          onAgent={(v) => setFilter({ agent: v })}
        />
      </PageHeader>
      <div className="mb-3">
        <Segmented
          label="Insight"
          variant="tab"
          value={tab}
          options={TABS.map((t) => ({ value: t, label: TAB_LABEL[t] }))}
          onChange={(t) => setFilter({ tab: t === TABS[0] ? "" : t })}
        />
      </div>
      {tab === "errors" && <ErrorsTab f={f} />}
      {tab === "prs" && <PRsTab f={f} />}
      {tab === "timing" && <TimingTab f={f} />}
      {tab === "compaction" && <CompactionTab f={f} />}
      {tab === "hooks" && <HooksTab f={f} />}
      {tab === "changes" && <ChangesTab f={f} />}
      {tab === "effort" && <EffortTab f={f} />}
    </div>
  );
}

const TAB_LABEL: Record<Tab, string> = {
  errors: "errors",
  prs: "pull requests",
  timing: "timing",
  compaction: "compaction",
  hooks: "hooks",
  changes: "lines changed",
  effort: "effort",
};

const ERROR_KINDS = "api_error,aborted,tool_denied";

// ErrorsTab: failed model requests, interrupted turns, refused tool
// calls, and which tools fail — each event a link into its transcript.
function ErrorsTab({ f }: { f: InsightFilters }) {
  const stats = useQuery({
    queryKey: ["eventStats", ERROR_KINDS, "label", f],
    queryFn: () => api.eventStats({ ...f, kind: ERROR_KINDS, group: "label" }),
  });
  const tools = useQuery({
    queryKey: ["toolStats", f],
    queryFn: () => api.toolStats({ ...f, limit: 1000 }),
  });
  const recent = useQuery({
    queryKey: ["sessionEvents", ERROR_KINDS, f],
    queryFn: () => api.sessionEvents({ ...f, kind: ERROR_KINDS, limit: 100 }),
  });
  if (stats.error) return <LoadError error={stats.error} />;
  const rows = stats.data ?? [];
  const byKind = (kind: string) => rows.filter((r) => r.kind === kind);
  const total = (kind: string) => byKind(kind).reduce((n, r) => n + r.count, 0);
  const toolRows = tools.data ?? [];
  const calls = toolRows.reduce((n, t) => n + t.calls, 0);
  const failed = toolRows.reduce((n, t) => n + t.errors, 0);
  const failing = toolRows.filter((t) => t.errors > 0).slice(0, 15);
  const bars = (kind: string) =>
    byKind(kind).map((r) => ({ label: r.group || "(none)", count: r.count }));

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatTile
          label="API errors"
          value={fmtCount(total("api_error"))}
          tone={total("api_error") > 0 ? "warn" : undefined}
        />
        <StatTile label="Aborted turns" value={fmtCount(total("aborted"))} />
        <StatTile
          label="Refused tool calls"
          value={fmtCount(total("tool_denied"))}
        />
        <StatTile
          label="Tool failure rate"
          value={calls > 0 ? `${((failed / calls) * 100).toFixed(1)}%` : "—"}
          detail={`${fmtCount(failed)} of ${fmtCount(calls)} calls`}
        />
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <Panel label="API errors by class">
          {stats.isLoading ? (
            <SkeletonRows rows={3} />
          ) : (
            <KindBars
              items={bars("api_error")}
              fmt={fmtCount}
              empty="No API errors in this range."
            />
          )}
        </Panel>
        <Panel label="Refused tool calls by reason">
          {stats.isLoading ? (
            <SkeletonRows rows={3} />
          ) : (
            <KindBars
              items={bars("tool_denied")}
              fmt={fmtCount}
              empty="No refused tool calls in this range."
            />
          )}
        </Panel>
      </div>
      <Panel label="Tools that fail most">
        {tools.error ? (
          <div className="px-3 py-3">
            <LoadError error={tools.error} />
          </div>
        ) : failing.length === 0 ? (
          <EmptyNote>No failed tool calls in this range.</EmptyNote>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-left font-mono text-micro text-ink-faint">
              <tr>
                <th className="px-3 py-1.5 font-normal">tool</th>
                <th className="px-3 py-1.5 text-right font-normal">calls</th>
                <th className="px-3 py-1.5 text-right font-normal">failed</th>
                <th className="px-3 py-1.5 text-right font-normal">rate</th>
                <th className="px-3 py-1.5 text-right font-normal">sessions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge font-mono text-xs tabular-nums">
              {failing.map((t) => (
                <tr key={`${t.name}/${t.kind}`}>
                  <td className="px-3 py-1.5">{t.name}</td>
                  <td className="px-3 py-1.5 text-right">
                    {fmtCount(t.calls)}
                  </td>
                  <td className="px-3 py-1.5 text-right text-warn">
                    {fmtCount(t.errors)}
                  </td>
                  <td className="px-3 py-1.5 text-right">
                    {((t.errors / t.calls) * 100).toFixed(1)}%
                  </td>
                  <td className="px-3 py-1.5 text-right text-ink-faint">
                    {fmtCount(t.sessions)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
      <Panel label="Recent">
        {recent.error ? (
          <div className="px-3 py-3">
            <LoadError error={recent.error} />
          </div>
        ) : recent.isLoading ? (
          <SkeletonRows rows={5} />
        ) : (
          <EventList events={recent.data ?? []} />
        )}
      </Panel>
    </div>
  );
}

// PRsTab: every pull request a session opened or checked, with the
// sessions behind it — the bridge from "what shipped" back to the
// conversation that produced it.
function PRsTab({ f }: { f: InsightFilters }) {
  const q = useQuery({
    queryKey: ["sessionEvents", "pr_link", f],
    queryFn: () => api.sessionEvents({ ...f, kind: "pr_link", limit: 1000 }),
  });
  if (q.error) return <LoadError error={q.error} />;
  if (q.isLoading) return <SkeletonRows rows={6} />;
  const prs = groupPRs(q.data ?? []);
  return (
    <Panel label={`${fmtCount(prs.length)} pull requests`}>
      {prs.length === 0 ? (
        <EmptyNote>No pull requests linked in this range.</EmptyNote>
      ) : (
        <ul className="divide-y divide-edge">
          {prs.map((pr) => (
            <li
              key={pr.url}
              className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2 text-sm"
            >
              <span
                title={fullWhen(pr.last)}
                className="w-32 shrink-0 font-mono text-meta text-ink-faint tabular-nums"
              >
                {fmtWhen(pr.last)}
              </span>
              <PRLink pr={pr} />
              <span className="flex min-w-0 flex-1 flex-wrap gap-x-3 gap-y-1">
                {pr.sessions.map((s) => (
                  <Link
                    key={`${s.agent}/${s.sessionId}`}
                    to="/sessions/$agent/$sessionId"
                    params={{ agent: s.agent, sessionId: s.sessionId }}
                    search={{ seq: s.messageSeq }}
                    className="inline-flex max-w-72 min-w-0 items-baseline gap-1.5 text-meta text-ink-dim hover:text-accent"
                  >
                    <AgentChip agent={s.agent} />
                    <span className="truncate">
                      {s.sessionTitle || s.sessionId.slice(0, 8)}
                    </span>
                  </Link>
                ))}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

// TimingTab: how long turns take, per agent, and where tool time goes.
// Tool timing covers calls whose result timestamp (or the agent's own
// figure) is known; the table says how many that is.
function TimingTab({ f }: { f: InsightFilters }) {
  const byAgent = useQuery({
    queryKey: ["eventStats", "turn", "agent", f],
    queryFn: () => api.eventStats({ ...f, kind: "turn", group: "agent" }),
  });
  const tools = useQuery({
    queryKey: ["toolStats", "total", f],
    queryFn: () => api.toolStats({ ...f, sort: "total", limit: 15 }),
  });
  const longest = useQuery({
    queryKey: ["sessionEvents", "turn", "duration", f],
    queryFn: () =>
      api.sessionEvents({ ...f, kind: "turn", sort: "duration", limit: 20 }),
  });
  if (byAgent.error) return <LoadError error={byAgent.error} />;
  const agents = byAgent.data ?? [];
  const turns = agents.reduce((n, a) => n + a.count, 0);
  const totalMs = agents.reduce((n, a) => n + (a.totalMs ?? 0), 0);
  const maxMs = Math.max(0, ...agents.map((a) => a.maxMs ?? 0));
  const toolRows = (tools.data ?? []).filter((t) => t.timed > 0);
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatTile label="Timed turns" value={fmtCount(turns)} />
        <StatTile
          label="Average turn"
          value={turns > 0 ? fmtDuration(totalMs / turns) : "—"}
        />
        <StatTile
          label="Longest turn"
          value={maxMs > 0 ? fmtDuration(maxMs) : "—"}
        />
        <StatTile label="Time in turns" value={fmtDuration(totalMs)} />
      </div>
      <Panel label="Average turn by agent">
        {byAgent.isLoading ? (
          <SkeletonRows rows={3} />
        ) : (
          <KindBars
            items={agents.map((a) => ({
              label: a.group,
              count: Math.round(a.avgMs ?? 0),
            }))}
            fmt={fmtDuration}
            empty="No timed turns in this range."
          />
        )}
      </Panel>
      <Panel label="Where tool time goes — call to result, including waits for your approval or answer">
        {tools.error ? (
          <div className="px-3 py-3">
            <LoadError error={tools.error} />
          </div>
        ) : toolRows.length === 0 ? (
          <EmptyNote>No timed tool calls in this range.</EmptyNote>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-left font-mono text-micro text-ink-faint">
              <tr>
                <th className="px-3 py-1.5 font-normal">tool</th>
                <th className="px-3 py-1.5 text-right font-normal">
                  timed calls
                </th>
                <th className="px-3 py-1.5 text-right font-normal">total</th>
                <th className="px-3 py-1.5 text-right font-normal">avg</th>
                <th className="px-3 py-1.5 text-right font-normal">max</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-edge font-mono text-xs tabular-nums">
              {toolRows.map((t) => (
                <tr key={`${t.name}/${t.kind}`}>
                  <td className="px-3 py-1.5">{t.name}</td>
                  <td className="px-3 py-1.5 text-right">
                    {fmtCount(t.timed)}
                    <span className="text-ink-faint">
                      {" "}
                      / {fmtCount(t.calls)}
                    </span>
                  </td>
                  <td className="px-3 py-1.5 text-right">
                    {fmtDuration(t.totalMs)}
                  </td>
                  <td className="px-3 py-1.5 text-right">
                    {fmtDuration(t.avgMs)}
                  </td>
                  <td className="px-3 py-1.5 text-right text-ink-faint">
                    {fmtDuration(t.maxMs)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
      <Panel label="Longest turns">
        {longest.error ? (
          <div className="px-3 py-3">
            <LoadError error={longest.error} />
          </div>
        ) : longest.isLoading ? (
          <SkeletonRows rows={5} />
        ) : (
          <EventList
            events={longest.data ?? []}
            empty="No timed turns in this range."
          />
        )}
      </Panel>
    </div>
  );
}

// CompactionTab: how often agents ran out of room and summarized, and how
// full the context was when they did. Each compaction opens its session's
// transcript, whose Context tab charts the fill around it.
function CompactionTab({ f }: { f: InsightFilters }) {
  const q = useQuery({
    queryKey: ["sessionEvents", "compaction", f],
    queryFn: () => api.sessionEvents({ ...f, kind: "compaction", limit: 1000 }),
  });
  if (q.error) return <LoadError error={q.error} />;
  if (q.isLoading) return <SkeletonRows rows={6} />;
  const rows = q.data ?? [];
  const pre = rows
    .map((r) => r.payload?.preTokens)
    .filter((n): n is number => typeof n === "number" && n > 0);
  const byAgent = new Map<string, number>();
  for (const r of rows) byAgent.set(r.agent, (byAgent.get(r.agent) ?? 0) + 1);
  const manual = rows.filter((r) => r.label === "manual").length;
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatTile label="Compactions" value={fmtCount(rows.length)} />
        <StatTile
          label="Sessions"
          value={fmtCount(
            new Set(rows.map((r) => `${r.agent}/${r.sessionId}`)).size,
          )}
        />
        <StatTile label="Manual" value={fmtCount(manual)} />
        <StatTile
          label="Average context before"
          value={
            pre.length
              ? fmtTokens(pre.reduce((a, b) => a + b, 0) / pre.length)
              : "—"
          }
        />
      </div>
      <Panel label="Compactions by agent">
        <KindBars
          items={[...byAgent].map(([label, count]) => ({ label, count }))}
          fmt={fmtCount}
          empty="No compactions in this range."
        />
      </Panel>
      <Panel label="Recent">
        <EventList events={rows} empty="No compactions in this range." />
      </Panel>
    </div>
  );
}

// HooksTab: what the configured hooks cost and where they fail — runs per
// hook event, each command's run count, failures and time, and the
// failing runs themselves. Hook output is never stored, only the outcome.
function HooksTab({ f }: { f: InsightFilters }) {
  const byEvent = useQuery({
    queryKey: ["eventStats", "hook", "label", f],
    queryFn: () => api.eventStats({ ...f, kind: "hook", group: "label" }),
  });
  const byCommand = useQuery({
    queryKey: ["eventStats", "hook", "detail", f],
    queryFn: () => api.eventStats({ ...f, kind: "hook", group: "detail" }),
  });
  const failing = useQuery({
    queryKey: ["sessionEvents", "hook", "failing", f],
    queryFn: () =>
      api.sessionEvents({
        ...f,
        kind: "hook",
        status: "error,blocked",
        limit: 100,
      }),
  });
  if (byEvent.error) return <LoadError error={byEvent.error} />;
  const events = byEvent.data ?? [];
  const runs = events.reduce((n, e) => n + e.count, 0);
  const failures = events.reduce((n, e) => n + e.failures, 0);
  const totalMs = events.reduce((n, e) => n + (e.totalMs ?? 0), 0);
  const commands = byCommand.data ?? [];
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatTile label="Hook runs" value={fmtCount(runs)} />
        <StatTile
          label="Failed or blocked"
          value={fmtCount(failures)}
          tone={failures > 0 ? "warn" : undefined}
        />
        <StatTile label="Time in hooks" value={fmtDuration(totalMs)} />
        <StatTile label="Commands" value={fmtCount(commands.length)} />
      </div>
      <Panel label="Runs by hook event">
        {byEvent.isLoading ? (
          <SkeletonRows rows={4} />
        ) : (
          <KindBars
            items={events.map((e) => ({ label: e.group, count: e.count }))}
            fmt={fmtCount}
            empty="No hook runs in this range."
          />
        )}
      </Panel>
      <Panel label="By command">
        {byCommand.error ? (
          <div className="px-3 py-3">
            <LoadError error={byCommand.error} />
          </div>
        ) : commands.length === 0 ? (
          <EmptyNote>No hook runs in this range.</EmptyNote>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left font-mono text-micro text-ink-faint">
                <tr>
                  <th className="px-3 py-1.5 font-normal">command</th>
                  <th className="px-3 py-1.5 text-right font-normal">runs</th>
                  <th className="px-3 py-1.5 text-right font-normal">failed</th>
                  <th className="px-3 py-1.5 text-right font-normal">avg</th>
                  <th className="px-3 py-1.5 text-right font-normal">max</th>
                  <th className="px-3 py-1.5 text-right font-normal">total</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-edge font-mono text-xs tabular-nums">
                {commands.map((c) => (
                  <tr key={c.group}>
                    <td
                      className="max-w-md truncate px-3 py-1.5"
                      title={c.group}
                    >
                      {c.group || "(no command recorded)"}
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      {fmtCount(c.count)}
                    </td>
                    <td
                      className={`px-3 py-1.5 text-right ${c.failures > 0 ? "text-warn" : "text-ink-faint"}`}
                    >
                      {fmtCount(c.failures)}
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      {fmtDuration(c.avgMs ?? 0)}
                    </td>
                    <td className="px-3 py-1.5 text-right text-ink-faint">
                      {fmtDuration(c.maxMs ?? 0)}
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      {fmtDuration(c.totalMs ?? 0)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
      <Panel label="Failed and blocked runs">
        {failing.error ? (
          <div className="px-3 py-3">
            <LoadError error={failing.error} />
          </div>
        ) : failing.isLoading ? (
          <SkeletonRows rows={4} />
        ) : (
          <EventList
            events={failing.data ?? []}
            empty="No failed hook runs in this range."
          />
        )}
      </Panel>
    </div>
  );
}

// ChangesTab: how much code the agents changed and where — lines added
// and removed per agent, and the most-changed files. Sizes come from the
// diffs agents record; changes without one are not counted.
function ChangesTab({ f }: { f: InsightFilters }) {
  const byAgent = useQuery({
    queryKey: ["changes", "agent", f],
    queryFn: () => api.changes({ ...f, group: "agent" }),
  });
  const files = useQuery({
    queryKey: ["changes", "file", f],
    queryFn: () => api.changes({ ...f, group: "file", limit: 50 }),
  });
  if (byAgent.error) return <LoadError error={byAgent.error} />;
  const agents = byAgent.data ?? [];
  const added = agents.reduce((n, a) => n + a.added, 0);
  const removed = agents.reduce((n, a) => n + a.removed, 0);
  const changes = agents.reduce((n, a) => n + a.changes, 0);
  const fileRows = files.data ?? [];
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatTile
          label="Lines added"
          value={<span className="text-ok">+{fmtCount(added)}</span>}
        />
        <StatTile
          label="Lines removed"
          value={<span className="text-warn">−{fmtCount(removed)}</span>}
        />
        <StatTile label="File changes" value={fmtCount(changes)} />
        <StatTile
          label="Net"
          value={`${added - removed >= 0 ? "+" : "−"}${fmtCount(Math.abs(added - removed))}`}
        />
      </div>
      <Panel label="Lines changed by agent">
        {byAgent.isLoading ? (
          <SkeletonRows rows={3} />
        ) : (
          <KindBars
            items={agents.map((a) => ({
              label: a.group,
              count: a.added + a.removed,
            }))}
            fmt={fmtCount}
            empty="No sized file changes in this range."
          />
        )}
      </Panel>
      <Panel label="Most-changed files">
        {files.error ? (
          <div className="px-3 py-3">
            <LoadError error={files.error} />
          </div>
        ) : fileRows.length === 0 ? (
          <EmptyNote>No sized file changes in this range.</EmptyNote>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left font-mono text-micro text-ink-faint">
                <tr>
                  <th className="px-3 py-1.5 font-normal">file</th>
                  <th className="px-3 py-1.5 text-right font-normal">
                    changes
                  </th>
                  <th className="px-3 py-1.5 text-right font-normal">added</th>
                  <th className="px-3 py-1.5 text-right font-normal">
                    removed
                  </th>
                  <th className="px-3 py-1.5 text-right font-normal">
                    sessions
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-edge font-mono text-xs tabular-nums">
                {fileRows.map((r) => (
                  <tr key={r.group}>
                    <td
                      className="max-w-lg truncate px-3 py-1.5"
                      title={r.group}
                    >
                      {shortPath(r.group)}
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      {fmtCount(r.changes)}
                    </td>
                    <td className="px-3 py-1.5 text-right text-ok">
                      +{fmtCount(r.added)}
                    </td>
                    <td
                      className={`px-3 py-1.5 text-right ${r.removed > 0 ? "text-warn" : "text-ink-faint"}`}
                    >
                      {r.removed > 0 ? `−${fmtCount(r.removed)}` : "0"}
                    </td>
                    <td className="px-3 py-1.5 text-right text-ink-faint">
                      {fmtCount(r.sessions)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </div>
  );
}
