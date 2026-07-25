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
import { api } from "./api";
import { cssColor, useResolvedTheme } from "./theme";
import { EmptyNote, Panel } from "./ui";

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

function dayMs(day: string): number {
  return Date.parse(`${day}T00:00:00Z`);
}

function buildOption(series: DaySeries[], pal: ChartPalette) {
  const days = Array.from(
    new Set(series.flatMap((s) => Array.from(s.byDay.keys()))),
  ).toSorted();

  // A little air at both ends so the first and last day's bar is drawn
  // whole rather than half-clipped by the grid edge — on a time axis the
  // extreme values land exactly ON the boundary.
  const stamps = days.map(dayMs);
  const lo = Math.min(...stamps);
  const hi = Math.max(...stamps);
  const pad = Math.max((hi - lo) * 0.02, 12 * 60 * 60 * 1000);

  return {
    backgroundColor: "transparent",
    grid: { left: 60, right: 16, top: 36, bottom: 52 },
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
        typeof v === "number" && v > 0 ? `$${v.toFixed(4)}` : "—",
    },
    // A TIME axis, not a category axis. As categories, every day the data
    // happened to contain sat one bar-width from the next, so a gap of a
    // year between two active days rendered exactly like a gap of one day
    // — the timeline was not to scale, and the shape of spending over
    // time is the entire reason this chart exists.
    xAxis: {
      type: "time",
      min: lo - pad,
      max: hi + pad,
      axisLine: { lineStyle: { color: pal.edge } },
      axisTick: { show: false },
      axisLabel: { color: pal.inkDim, fontSize: 11, hideOverlap: true },
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
        height: 18,
        bottom: 8,
        borderColor: pal.edge,
        backgroundColor: pal.surface,
        fillerColor: withAlpha(pal.accent, 0.15),
        handleStyle: { color: pal.inkDim },
        // The data shadow drew a filled silhouette of the series inside
        // the slider; at a handful of points it read as a stray triangle
        // rather than a preview of anything.
        showDataShadow: false,
        textStyle: { color: pal.inkDim, fontSize: 10 },
      },
    ],
    series: series.map((s) => ({
      name: s.agent,
      type: "bar",
      stack: "cost",
      data: days.map((d) => [dayMs(d), s.byDay.get(d) ?? 0]),
      color: pal.agents[s.agent],
      // 1px surface gap between stacked segments so adjacency never
      // rides on hue alone.
      itemStyle: { borderColor: pal.surface, borderWidth: 1 },
      barMaxWidth: 28,
      // Over a year-wide axis a single day is a sliver; this keeps it on
      // screen without inflating it into a claim about its duration.
      barMinWidth: 3,
      emphasis: { focus: "series" },
    })),
  };
}

function downloadCSV(series: DaySeries[]) {
  const days = Array.from(
    new Set(series.flatMap((s) => Array.from(s.byDay.keys()))),
  ).toSorted();
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
    if (!el.current) return undefined;
    if (chart.current && chart.current.getDom() !== el.current) {
      chart.current.dispose();
      chart.current = null;
    }
    if (!option) {
      chart.current?.clear();
      return undefined;
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

// CostTimeline is the cost explorer's one persistent graph: daily spend
// stacked by agent, on a real time axis, with wheel + slider zoom,
// following the page's date/agent/model filters. The rollup table below
// it stays the accessible/table view of the same data.
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

  return (
    <Panel
      label="Daily cost by agent"
      className="mb-4"
      action={
        series.length > 0 && (
          <button
            type="button"
            onClick={() => downloadCSV(series)}
            className="font-mono text-meta text-ink-faint hover:text-ink"
          >
            export CSV
          </button>
        )
      }
    >
      {/* The container always mounts: unmounting it on an empty result
          made the whole page jump by the chart's height every time a
          filter emptied the range. */}
      <div
        ref={el}
        className={`h-64 w-full ${series.length === 0 ? "hidden" : ""}`}
        role="img"
        aria-label="Daily cost stacked by agent; the table below holds the same data"
      />
      {series.length === 0 && (
        <EmptyNote>
          {isLoading ? "Loading…" : "No cost recorded in this range."}
        </EmptyNote>
      )}
    </Panel>
  );
}
