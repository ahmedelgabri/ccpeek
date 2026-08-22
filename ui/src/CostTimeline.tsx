import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
} from "react";
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
import { api, fmtCostDecimal, fmtCostExact } from "./api";
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

type DayValue = { cost: number; incomplete: boolean };
type DaySeries = { agent: string; byDay: Map<string, DayValue> };
type DragSelection = { startX: number; currentX: number };

// Shared by ECharts and the pointer-selection overlay so a drag is accepted
// only over the actual plot, never over its axes or the zoom slider.
const CHART_GRID = { left: 60, right: 16, top: 36, bottom: 52 } as const;
const MIN_DRAG_PX = 4;

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
            incomplete: Boolean(r.hasUnpriced),
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
    grid: CHART_GRID,
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
        typeof v === "number" && v > 0 ? fmtCostExact(v) : "—",
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
      // Wheel zoom remains available, but mouse-drag panning is disabled so
      // an unmodified drag can always mean selecting a visible range.
      { type: "inside", moveOnMouseMove: false },
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
          // null means this agent had no row that day. A numeric zero is
          // reserved for incomplete-cost usage that needs the warning-height
          // marker; barMinHeight must never turn absence into a phantom bar.
          value: [dayMs(d), value ? value.cost : null],
          itemStyle:
            value?.incomplete && !(value.cost > 0)
              ? { color: pal.warn, borderColor: pal.surface, borderWidth: 1 }
              : undefined,
        };
      }),
      color: pal.agents[s.agent],
      // 1px surface gap between stacked segments so adjacency never
      // rides on hue alone.
      itemStyle: { borderColor: pal.surface, borderWidth: 1 },
      barMaxWidth: 28,
      // Gives zero-dollar incomplete days a visible warning marker without
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
    ...series.flatMap((s) => [s.agent, `${s.agent}_has_incomplete_cost`]),
  ].join(",");
  const lines = days.map((d) =>
    [
      d,
      ...series.flatMap((s) => {
        const value = s.byDay.get(d);
        return [
          fmtCostDecimal(value?.cost ?? 0),
          value?.incomplete ? "1" : "0",
        ];
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

  return chart;
}

// CostTimeline is the cost explorer's one persistent graph: daily spend
// stacked by agent, on a real time axis, with range-select, wheel, and slider zoom,
// following the page's date/agent/model filters. The rollup table below
// it stays the accessible/table view of the same data.
export function CostTimeline({ since, until, agent, model }: TimelineFilters) {
  const { data, isLoading } = useQuery({
    queryKey: ["usage", "daily-by-agent", since, until, agent, model],
    queryFn: () => fetchDailyCostByAgent({ since, until, agent, model }),
    placeholderData: (prev) => prev,
  });
  const series = useMemo(() => data ?? [], [data]);
  const hasIncomplete = series.some((s) =>
    Array.from(s.byDay.values()).some((v) => v.incomplete),
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
  const chart = useEChart(el, option, [series, theme]);
  const [drag, setDrag] = useState<DragSelection | null>(null);
  const [zoomed, setZoomed] = useState(false);

  // Keep the contextual reset action in sync with direct selection, wheel,
  // and slider zoom. setOption replaces the old dataZoom state whenever the
  // filters or underlying series change, so sync immediately as well as on
  // ECharts' datazoom event.
  useEffect(() => {
    const instance = chart.current;
    if (!instance) return undefined;
    const syncZoom = () => {
      const raw = instance.getOption().dataZoom;
      const zooms = Array.isArray(raw) ? raw : raw ? [raw] : [];
      setZoomed(
        zooms.some(
          (zoom) =>
            (typeof zoom.start === "number" && zoom.start > 0.001) ||
            (typeof zoom.end === "number" && zoom.end < 99.999),
        ),
      );
    };
    instance.on("datazoom", syncZoom);
    syncZoom();
    return () => {
      instance.off("datazoom", syncZoom);
    };
  }, [chart, series, theme]);

  const pointInPlot = (event: ReactPointerEvent<HTMLDivElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    return {
      x: Math.min(
        Math.max(event.clientX - rect.left, CHART_GRID.left),
        rect.width - CHART_GRID.right,
      ),
      y: event.clientY - rect.top,
      height: rect.height,
    };
  };

  const startSelection = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    const point = pointInPlot(event);
    if (
      point.y < CHART_GRID.top ||
      point.y > point.height - CHART_GRID.bottom
    ) {
      return;
    }
    event.currentTarget.setPointerCapture(event.pointerId);
    setDrag({ startX: point.x, currentX: point.x });
  };

  const moveSelection = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!drag || !event.currentTarget.hasPointerCapture(event.pointerId)) return;
    const point = pointInPlot(event);
    setDrag((current) =>
      current ? { ...current, currentX: point.x } : current,
    );
  };

  const finishSelection = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!drag || !event.currentTarget.hasPointerCapture(event.pointerId)) return;
    const endX = pointInPlot(event).x;
    event.currentTarget.releasePointerCapture(event.pointerId);
    const left = Math.min(drag.startX, endX);
    const right = Math.max(drag.startX, endX);
    setDrag(null);
    if (right - left < MIN_DRAG_PX || !chart.current) return;
    const startValue = chart.current.convertFromPixel({ xAxisIndex: 0 }, left);
    const endValue = chart.current.convertFromPixel({ xAxisIndex: 0 }, right);
    chart.current.dispatchAction({ type: "dataZoom", startValue, endValue });
  };

  const resetZoom = () =>
    chart.current?.dispatchAction({ type: "dataZoom", start: 0, end: 100 });

  return (
    <Panel
      label="Daily cost by agent"
      className="mb-4"
      action={
        series.length > 0 && (
          <span className="flex items-center gap-3">
            {hasIncomplete && (
              <span
                className="font-mono text-meta text-warn"
                title="Warning-height markers are days with unpriced usage"
              >
                ● incomplete cost
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
      <div className="relative">
        <div
          ref={el}
          className={`h-64 w-full cursor-crosshair select-none ${series.length === 0 ? "hidden" : ""}`}
          role="img"
          aria-label="Daily API-equivalent cost stacked by agent; drag across the plot to zoom into a time range; warning markers indicate incomplete cost coverage; the table below holds the same data"
          style={{ touchAction: "pan-y" }}
          onPointerDown={startSelection}
          onPointerMove={moveSelection}
          onPointerUp={finishSelection}
          onPointerCancel={() => setDrag(null)}
        />
        {drag && (
          <div
            aria-hidden
            className="pointer-events-none absolute border border-accent bg-accent/15"
            style={{
              left: Math.min(drag.startX, drag.currentX),
              top: CHART_GRID.top,
              bottom: CHART_GRID.bottom,
              width: Math.abs(drag.currentX - drag.startX),
            }}
          />
        )}
        {zoomed && (
          <button
            type="button"
            onClick={resetZoom}
            aria-label="Reset chart zoom"
            title="Reset chart zoom"
            className="absolute cursor-pointer top-3 right-3 z-10 flex h-[18px] w-[18px] items-center justify-center text-ink-dim hover:text-accent"
          >
            {/* ECharts' original dataZoom back icon, retained while direct
                drag selection replaces its opt-in zoom control. */}
            <svg
              aria-hidden
              viewBox="0 0 60 60"
              className="h-3.5 w-3.5 fill-none stroke-current"
            >
              <path
                d="M22 1.4 9.9 13.5l12.3 12.3M10.3 13.5h44.6v44.6H10.3v-26"
                strokeWidth="4"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          </button>
        )}
      </div>
      {series.length === 0 && (
        <EmptyNote>
          {isLoading ? "Loading…" : "No cost recorded in this range."}
        </EmptyNote>
      )}
    </Panel>
  );
}
