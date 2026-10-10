import { useQuery } from "@tanstack/react-query";
import { useSearch } from "@tanstack/react-router";
import { api, fmtCount, type InsightFilters } from "../api";
import {
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
import { EventList } from "./insights/events";

const TABS = ["errors"] as const;
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
          options={TABS.map((t) => ({ value: t, label: t }))}
          onChange={(t) => setFilter({ tab: t === TABS[0] ? "" : t })}
        />
      </div>
      {tab === "errors" && <ErrorsTab f={f} />}
    </div>
  );
}

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
