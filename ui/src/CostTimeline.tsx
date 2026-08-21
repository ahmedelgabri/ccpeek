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

// Canvas renderers can't consume CSS variables, so the palette resolves
// from the design tokens at option-build time. Known agents retain their
// validated permanent colors; newly indexed adapters receive the neutral ink
// color instead of disappearing from a hardcoded roster.
const AGENT_COLOR_TOKEN: Record<string, string> = {
  "claude-code": "--color-agent-claude",
  pi: "--color-agent-pi",
  codex: "--color-agent-codex",
  opencode: "--color-agent-opencode",
  cursor: "--color-agent-cursor",
};

function chartPalette(agents: string[]) {
  const fallback = cssColor("--color-ink-faint");
  return {
    agents: Object.fromEntries(
      agents.map((a) => [
        a,
        AGENT_COLOR_TOKEN[a] ? cssColor(AGENT_COLOR_TOKEN[a]) : fallback,
      ]),
    ) as Record<string, string>,
    surface: cssColor("--color-surface-1"),
    surface2: cssColor("--color-surface-2"),
    ink: cssColor("--color-ink"),
    inkDim: cssColor("--color-ink-dim"),
    inkFaint: cssColor("--color-ink-faint"),
    edge: cssColor("--color-edge"),
    accent: cssColor("--color-accent"),
    warn: cssColor("--color-warn"),
  };
}
type ChartPalette = ReturnType<typeof chartPalette>;

function withAlpha(rgb: string, alpha: number): string {
  return rgb.replace("rgb(", "rgba(").replace(")", `, ${alpha})`);
}

type DayValue = { cost: number; unpriced: boolean };
type DaySeries = { agent: string; byDay: Map<string, DayValue> };

interface TimelineFilters {
  since?: string;
  until?: string;
  agent?: string;
  model?: string;
}

async function fetchDailyCostByAgent(f: TimelineFilters): Promise<DaySeries[]> {
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
      const byDay = new Map<string, DayValue>();
      for (const r of rows ?? []) {
        // An entirely unpriceable day is still usage. Keep it on the time
        // axis and render a warning-height marker rather than deleting it.
        if (r.costUSD > 0 || r.hasUnpriced) {
          byDay.set(r.group, {
            cost: r.costUSD,
            unpriced: Boolean(r.hasUnpriced),
          });
        }
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
      data: days.map((d) => {
        const value = s.byDay.get(d);
        return {
          value: [dayMs(d), value?.cost ?? 0],
          itemStyle:
            value?.unpriced && !(value.cost > 0)
              ? { color: pal.warn, borderColor: pal.surface, borderWidth: 1 }
              : undefined,
        };
      }),
      color: pal.agents[s.agent],
      // 1px surface gap between stacked segments so adjacency never
      // rides on hue alone.
      itemStyle: { borderColor: pal.surface, borderWidth: 1 },
      barMaxWidth: 28,
      // Gives zero-dollar unpriced days a visible warning marker without
      // inventing a dollar value.
      barMinHeight: 3,
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
  const header = [
    "day",
    ...series.flatMap((s) => [s.agent, `${s.agent}_has_unpriced`]),
  ].join(",");
  const lines = days.map((d) =>
    [
      d,
      ...series.flatMap((s) => {
        const value = s.byDay.get(d);
        return [(value?.cost ?? 0).toFixed(6), value?.unpriced ? "1" : "0"];
      }),
    ].join(","),
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
  const hasUnpriced = series.some((s) =>
    Array.from(s.byDay.values()).some((v) => v.unpriced),
  );

  const el = useRef<HTMLDivElement>(null);
  const theme = useResolvedTheme();
  // Memoized on the same inputs useEChart applies it for. Built in the
  // render body it was rebuilt on EVERY parent render — and the Usage
  // page's tooltip state lives at the page root, so a pointer crossing the
  // table re-rendered this. chartPalette() resolves 12 CSS variables by
  // appending a probe span and reading getComputedStyle, i.e. 12 forced
  // style recalculations per hover, on top of a full series rebuild.
  const option = useMemo(
    () =>
      series.length > 0
        ? buildOption(series, chartPalette(series.map((s) => s.agent)))
        : null,
    [series, theme],
  );
  useEChart(el, option, [series, theme]);

  return (
    <Panel
      label="Daily cost by agent"
      className="mb-4"
      action={
        series.length > 0 && (
          <span className="flex items-center gap-3">
            {hasUnpriced && (
              <span
                className="font-mono text-meta text-warn"
                title="Warning-height markers are days with usage that has no resolvable model or bucket rate; their dollar total is a lower bound"
              >
                ● unpriced usage
              </span>
            )}
            <button
              type="button"
              onClick={() => downloadCSV(series)}
              className="font-mono text-meta text-ink-faint hover:text-ink"
            >
              export CSV
            </button>
          </span>
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
        aria-label="Daily API-equivalent cost stacked by agent; warning markers indicate unpriced usage; the table below holds the same data"
      />
      {series.length === 0 && (
        <EmptyNote>
          {isLoading ? "Loading…" : "No cost recorded in this range."}
        </EmptyNote>
      )}
    </Panel>
  );
}
