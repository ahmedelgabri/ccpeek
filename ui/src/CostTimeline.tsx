import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, fmtCostDecimal, fmtCostExact } from "./api";
import {
  StackedTimeline,
  type TimelinePoint,
  type TimelineSeries,
} from "./StackedTimeline";
import { cssColor } from "./theme";

// Known agents keep their validated permanent colors; newly indexed
// adapters receive the neutral ink color instead of disappearing from a
// hardcoded roster.
const AGENT_COLOR_TOKEN: Record<string, string> = {
  "claude-code": "--color-agent-claude",
  pi: "--color-agent-pi",
  codex: "--color-agent-codex",
  opencode: "--color-agent-opencode",
  cursor: "--color-agent-cursor",
};

function agentColor(agent: string): string {
  return cssColor(AGENT_COLOR_TOKEN[agent] ?? "--color-ink-faint");
}

interface TimelineFilters {
  since?: string;
  until?: string;
  agent?: string;
  model?: string;
}

async function fetchDailyCostByAgent(
  f: TimelineFilters,
): Promise<TimelineSeries[]> {
  const agents = f.agent
    ? [f.agent]
    : ((await api.stats()).agents ?? []).map((a) => a.agent);
  const results = await Promise.all(
    agents.map(async (agent) => {
      const rows = await api.usage({
        group: "day",
        agent,
        since: f.since,
        until: f.until,
        model: f.model,
      });
      const byDay = new Map<string, TimelinePoint>();
      for (const r of rows ?? []) {
        // An entirely unpriceable day is still usage. Keep it on the time
        // axis and render a warning-height marker rather than deleting it.
        if (r.costUSD > 0 || r.hasUnpriced) {
          byDay.set(r.group, {
            value: r.costUSD,
            incomplete: Boolean(r.hasUnpriced),
          });
        }
      }
      return { name: agent, byDay };
    }),
  );
  return results.filter((s) => s.byDay.size > 0);
}

const fmtAxisDollars = (v: number) => `$${v}`;

// CostTimeline is the cost explorer's one persistent graph: daily spend
// stacked by agent, following the page's date/agent/model filters. The
// rollup table below it stays the accessible/table view of the same data.
export function CostTimeline({ since, until, agent, model }: TimelineFilters) {
  const { data, isLoading } = useQuery({
    queryKey: ["usage", "daily-by-agent", since, until, agent, model],
    queryFn: () => fetchDailyCostByAgent({ since, until, agent, model }),
    placeholderData: (prev) => prev,
  });
  const series = useMemo(() => data ?? [], [data]);
  return (
    <StackedTimeline
      label="Daily cost by agent"
      series={series}
      loading={isLoading}
      emptyText="No cost recorded in this range."
      ariaLabel="Daily API-equivalent cost stacked by agent; drag across the plot to zoom into a time range; warning markers indicate incomplete cost coverage; the table below holds the same data"
      colorOf={agentColor}
      valueFormat={fmtCostExact}
      axisFormat={fmtAxisDollars}
      csv={{
        filename: "ccpeek-daily-cost.csv",
        value: fmtCostDecimal,
        incompleteColumns: true,
      }}
      incompleteNote={
        <span
          className="font-mono text-meta text-warn"
          title="Warning-height markers are days with unpriced usage"
        >
          ● incomplete cost
        </span>
      }
    />
  );
}
