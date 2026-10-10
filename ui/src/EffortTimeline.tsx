import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, fmtCount, type InsightFilters } from "./api";
import { levelLabel, levelRank, levelWeight } from "./pages/insights/levels";
import {
  StackedTimeline,
  type TimelinePoint,
  type TimelineSeries,
} from "./StackedTimeline";
import { cssColor } from "./theme";

// Canvas cannot evaluate color-mix(), so a level's shade is mixed here
// from the resolved accent and surface colors, in sRGB.
function rgb(color: string): number[] {
  return (color.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
}

function levelColor(label: string): string {
  // Series are named by their display label; map back to the level.
  const level = label === levelLabel("") ? "" : label;
  const weight = levelWeight(level);
  if (weight === null) return cssColor("--color-edge-strong");
  const accent = rgb(cssColor("--color-accent"));
  const surface = rgb(cssColor("--color-surface-1"));
  const mixed = accent.map((a, i) =>
    Math.round(a * weight + surface[i] * (1 - weight)),
  );
  return `rgb(${mixed.join(", ")})`;
}

const fmtRequests = (v: number) => `${fmtCount(v)} requests`;
const fmtAxisCount = (v: number) => fmtCount(v);

// EffortTimeline is the Usage page's daily explorer applied to effort:
// model requests per day, stacked by the effort or thinking level they ran
// at, with the same zoom, range-select, and CSV export. The By level
// table beside it is the accessible view.
export function EffortTimeline(f: InsightFilters) {
  const { data, isLoading } = useQuery({
    queryKey: ["effort", "day", f],
    queryFn: () => api.effort({ ...f, group: "day" }),
    placeholderData: (prev) => prev,
  });
  const series = useMemo<TimelineSeries[]>(() => {
    const byLevel = new Map<string, Map<string, TimelinePoint>>();
    for (const r of data ?? []) {
      if (!r.day || r.requests === 0) continue;
      const days = byLevel.get(r.level) ?? new Map<string, TimelinePoint>();
      days.set(r.day, { value: r.requests });
      byLevel.set(r.level, days);
    }
    return [...byLevel.keys()]
      .toSorted((a, b) => levelRank(a) - levelRank(b))
      .map((level) => ({
        name: levelLabel(level),
        byDay: byLevel.get(level)!,
      }));
  }, [data]);
  return (
    <StackedTimeline
      label="Requests per day by level"
      series={series}
      loading={isLoading}
      emptyText="No model requests in this range."
      ariaLabel="Model requests per day stacked by effort level; drag across the plot to zoom into a time range; the By level table below holds the totals"
      colorOf={levelColor}
      valueFormat={fmtRequests}
      axisFormat={fmtAxisCount}
      csv={{ filename: "ccpeek-daily-effort.csv", value: String }}
    />
  );
}
