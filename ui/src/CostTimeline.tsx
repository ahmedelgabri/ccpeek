import { useEffect, useMemo, useRef, type RefObject } from "react";
import { useQuery } from "@tanstack/react-query";
import * as echarts from "echarts/core";
import { BarChart } from "echarts/charts";
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import { api, shortPath, type UsageRow } from "./api";
import { cssColor, useResolvedTheme } from "./theme";

echarts.use([
  BarChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  DataZoomComponent,
  CanvasRenderer,
]);

// Color follows the entity: each agent owns its slot forever, so a day
// without (say) pi never repaints the survivors. The set is validated
// (scripts in the dataviz method) against the surface-1 background:
// worst adjacent CVD ΔE 15.7, all ≥3:1 contrast.
const AGENTS = ["claude-code", "pi", "codex", "opencode", "cursor"];

// Canvas renderers can't consume CSS variables, so the palette resolves
// from the design tokens at option-build time — the components re-build
// on theme changes (useResolvedTheme) to pick up the other scheme.
function chartPalette() {
  return {
    agents: Object.fromEntries(
      AGENTS.map((a) => [a, cssColor(`--color-agent-${a.split("-")[0]}`)]),
    ) as Record<string, string>,
    surface: cssColor("--color-surface-1"),
    surface2: cssColor("--color-surface-2"),
    ink: cssColor("--color-ink"),
    inkDim: cssColor("--color-ink-dim"),
    inkFaint: cssColor("--color-ink-faint"),
    edge: cssColor("--color-edge"),
    accent: cssColor("--color-accent"),
  };
}
type ChartPalette = ReturnType<typeof chartPalette>;

function withAlpha(rgb: string, alpha: number): string {
  return rgb.replace("rgb(", "rgba(").replace(")", `, ${alpha})`);
}

type DaySeries = { agent: string; byDay: Map<string, number> };

interface TimelineFilters {
  since?: string;
  until?: string;
  agent?: string;
  model?: string;
}

async function fetchDailyCostByAgent(
  f: TimelineFilters,
): Promise<DaySeries[]> {
  const agents = f.agent ? [f.agent] : AGENTS;
  const results = await Promise.all(
    agents.map(async (agent) => {
      const rows = await api.usage({
        group: "day",
        agent,
        since: f.since,
        until: f.until,
        model: f.model,
      });
      const byDay = new Map<string, number>();
      for (const r of rows ?? []) {
        if (r.costUSD > 0) byDay.set(r.group, r.costUSD);
      }
      return { agent, byDay };
    }),
  );
  return results.filter((s) => s.byDay.size > 0);
}

function buildOption(series: DaySeries[], pal: ChartPalette) {
  const days = Array.from(
    new Set(series.flatMap((s) => Array.from(s.byDay.keys()))),
  ).sort();

  return {
    backgroundColor: "transparent",
    grid: { left: 56, right: 16, top: 36, bottom: 56 },
    legend: {
      top: 0,
      left: 0,
      icon: "roundRect",
      itemWidth: 10,
      itemHeight: 10,
      textStyle: { color: pal.inkDim, fontSize: 12 },
    },
    tooltip: {
      trigger: "axis",
      axisPointer: { type: "shadow" },
      backgroundColor: pal.surface2,
      borderColor: pal.edge,
      textStyle: { color: pal.ink, fontSize: 12 },
      valueFormatter: (v: unknown) =>
        typeof v === "number" && v > 0 ? `$${v.toFixed(2)}` : "—",
    },
    xAxis: {
      type: "category",
      data: days,
      axisLine: { lineStyle: { color: pal.edge } },
      axisTick: { show: false },
      axisLabel: { color: pal.inkDim, fontSize: 11 },
    },
    yAxis: {
      type: "value",
      axisLabel: {
        color: pal.inkDim,
        fontSize: 11,
        formatter: (v: number) => `$${v}`,
      },
      splitLine: { lineStyle: { color: pal.edge } },
    },
    dataZoom: [
      { type: "inside" },
      {
        type: "slider",
        height: 20,
        bottom: 8,
        borderColor: pal.edge,
        backgroundColor: pal.surface,
        fillerColor: withAlpha(pal.accent, 0.15),
        handleStyle: { color: pal.inkDim },
        textStyle: { color: pal.inkDim, fontSize: 10 },
      },
    ],
    series: series.map((s) => ({
      name: s.agent,
      type: "bar",
      stack: "cost",
      data: days.map((d) => s.byDay.get(d) ?? 0),
      color: pal.agents[s.agent],
      // 2px surface gap between stacked segments so adjacency never
      // rides on hue alone.
      itemStyle: { borderColor: pal.surface, borderWidth: 1 },
      barMaxWidth: 28,
      emphasis: { focus: "series" },
    })),
  };
}

// GroupBars is the graph for the non-day groupings (model, agent,
// project): horizontal cost bars, top rows first. Bars wear the fixed
// agent colors when the category IS an agent (identity); otherwise a
// single accent hue (magnitude across categories).
export function GroupBars({
  rows,
  group,
}: {
  rows: UsageRow[];
  group: string;
}) {
  const el = useRef<HTMLDivElement>(null);
  const theme = useResolvedTheme();
  const pal = chartPalette();
  const top = rows
    .filter((r) => r.costUSD > 0)
    .slice(0, 20)
    .reverse(); // echarts y-axis draws bottom-up

  const option =
    top.length === 0
      ? null
      : {
      backgroundColor: "transparent",
      grid: { left: 170, right: 48, top: 8, bottom: 24 },
      tooltip: {
        trigger: "axis",
        axisPointer: { type: "shadow" },
        backgroundColor: pal.surface2,
        borderColor: pal.edge,
        textStyle: { color: pal.ink, fontSize: 12 },
        valueFormatter: (v: unknown) =>
          typeof v === "number" ? `$${v.toFixed(2)}` : "",
      },
      xAxis: {
        type: "value",
        axisLabel: {
          color: pal.inkDim,
          fontSize: 11,
          formatter: (v: number) => `$${v}`,
        },
        splitLine: { lineStyle: { color: pal.edge } },
      },
      yAxis: {
        type: "category",
        data: top.map((r) => r.group || "(none)"),
        axisLine: { lineStyle: { color: pal.edge } },
        axisTick: { show: false },
        axisLabel: {
          color: pal.inkDim,
          fontSize: 11,
          // Paths differentiate at the tail: shorten the home prefix and
          // truncate from the left, never into "/Users/ahmed/code/…".
          formatter: (v: string) => {
            const label = group === "project" ? shortPath(v) : v;
            return label.length > 26 ? "…" + label.slice(-25) : label;
          },
        },
      },
      series: [
        {
          type: "bar",
          data: top.map((r) => ({
            value: r.costUSD,
            itemStyle: {
              color:
                group === "agent"
                  ? (pal.agents[r.group] ?? pal.inkFaint)
                  : pal.accent,
            },
          })),
          barMaxWidth: 18,
          itemStyle: { borderRadius: [0, 4, 4, 0] },
          label: {
            show: true,
            position: "right",
            color: pal.inkDim,
            fontSize: 10,
            formatter: ({ value }: { value: number }) => `$${value.toFixed(2)}`,
          },
        },
      ],
    };
  useEChart(el, option, [rows, group, theme]);

  if (top.length === 0) return null;
  return (
    <div className="mb-4 rounded-md border border-edge bg-surface-1 p-4">
      <h2 className="mb-1 text-sm font-medium text-ink-dim">
        Cost by {group}
      </h2>
      <div
        ref={el}
        className="overflow-hidden"
        style={{ height: Math.max(top.length * 26 + 60, 140) }}
        role="img"
        aria-label={`Cost by ${group}; the table below holds the same data`}
      />
    </div>
  );
}

function downloadCSV(series: DaySeries[]) {
  const days = Array.from(
    new Set(series.flatMap((s) => Array.from(s.byDay.keys()))),
  ).sort();
  const header = ["day", ...series.map((s) => s.agent)].join(",");
  const lines = days.map((d) =>
    [d, ...series.map((s) => (s.byDay.get(d) ?? 0).toFixed(6))].join(","),
  );
  const blob = new Blob([[header, ...lines].join("\n") + "\n"], {
    type: "text/csv",
  });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = "ccpeek-daily-cost.csv";
  a.click();
  URL.revokeObjectURL(a.href);
}

// useEChart binds an ECharts instance to a container that may unmount
// and remount (filter changes swap queries and views). The instance is
// re-created whenever it is bound to a stale DOM node — reusing one
// across remounts renders into a detached element, i.e. a blank chart —
// and options apply with notMerge so removed series actually disappear.
function useEChart(
  el: RefObject<HTMLDivElement | null>,
  option: Parameters<echarts.ECharts["setOption"]>[0] | null,
  deps: unknown[],
) {
  const chart = useRef<echarts.ECharts>(null);

  useEffect(() => {
    if (!el.current) return;
    if (chart.current && chart.current.getDom() !== el.current) {
      chart.current.dispose();
      chart.current = null;
    }
    if (!option) {
      chart.current?.clear();
      return;
    }
    chart.current ??= echarts.init(el.current);
    chart.current.setOption(option, true);
    // The container's height follows the data (bar count); without an
    // explicit resize the canvas keeps its old dimensions and paints
    // outside the panel.
    chart.current.resize();
    const onResize = () => chart.current?.resize();
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  useEffect(
    () => () => {
      chart.current?.dispose();
      chart.current = null;
    },
    [],
  );
}

// CostTimeline is the cost explorer: daily spend stacked by agent, with
// wheel + slider zoom (docs/v2-plan.md §7 P2), following the page's
// date/agent/model filters. The rollup table below it stays the
// accessible/table view of the same data.
export function CostTimeline({ since, until, agent, model }: TimelineFilters) {
  const { data, isLoading } = useQuery({
    queryKey: ["usage", "daily-by-agent", since, until, agent, model],
    queryFn: () => fetchDailyCostByAgent({ since, until, agent, model }),
    placeholderData: (prev) => prev,
  });
  const series = useMemo(() => data ?? [], [data]);

  const el = useRef<HTMLDivElement>(null);
  const theme = useResolvedTheme();
  useEChart(el, series.length > 0 ? buildOption(series, chartPalette()) : null, [
    series,
    theme,
  ]);

  if (series.length === 0 && !isLoading) return null;

  return (
    <div className="mb-4 rounded-lg border border-edge bg-surface-1 p-4">
      <div className="mb-1 flex items-baseline">
        <h2 className="text-sm font-medium text-ink-dim">
          Daily cost by agent
        </h2>
        <button
          onClick={() => downloadCSV(series)}
          className="ml-auto text-xs text-ink-dim hover:text-ink"
        >
          Export CSV
        </button>
      </div>
      <div ref={el} className="h-72 w-full" role="img" aria-label="Daily cost stacked by agent; the table below holds the same data" />
    </div>
  );
}
