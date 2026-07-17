import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  api,
  fmtCost,
  fmtTokens,
  fmtWhen,
  shortPath,
  totalTokens,
  type DayActivity,
} from "../api";
import {
  AgentChip,
  AgentDot,
  EmptyNote,
  KindBars,
  Panel,
  SkeletonRows,
  SkeletonTiles,
  Sparkline,
  StatTile,
  useTooltip,
} from "../ui";

// Overview is the instrument panel: headline counters, the activity
// heatmap, and the relation facets (agents, workspaces, latest sessions,
// latest file edits) — every figure links into its detail view.
export function OverviewPage() {
  const stats = useQuery({ queryKey: ["stats"], queryFn: api.stats });
  const recent = useQuery({
    queryKey: ["sessions", "recent"],
    queryFn: () => api.sessions({ limit: "12" }),
  });

  const st = stats.data;
  if (stats.isLoading)
    return (
      <div className="space-y-4">
        <SkeletonTiles />
        <SkeletonRows rows={8} />
      </div>
    );
  if (!st) return <EmptyNote>No data indexed yet.</EmptyNote>;

  const last30 = (st.activity ?? []).slice(-30);

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
        <StatTile
          label="Sessions"
          value={String(st.sessions)}
          to="/sessions"
          spark={<Sparkline values={last30.map((d) => d.sessions)} />}
        />
        <StatTile
          label="Cost / month"
          value={fmtCost(st.costMonthUSD)}
          detail={`${fmtCost(st.costUSD)} all time`}
          tone="ok"
          to="/usage"
          spark={<Sparkline values={last30.map((d) => d.costUSD)} />}
        />
        <StatTile
          label="Tokens"
          value={fmtTokens(st.tokens)}
          detail={`${fmtTokens(st.messages)} messages`}
          to="/usage"
        />
        <StatTile
          label="Commands"
          value={fmtTokens(st.commands)}
          detail={`${fmtTokens(st.toolCalls)} tool calls`}
          to="/commands"
        />
        <StatTile
          label="Artifacts"
          value={String(st.artifacts)}
          to="/artifacts"
        />
        <StatTile
          label="Scan findings"
          value={String(st.scanFindings)}
          tone={st.scanFindings > 0 ? "warn" : undefined}
          to="/scan"
        />
      </div>

      <Panel label="Activity — sessions per day, last 52 weeks">
        <div className="flex justify-center overflow-x-auto px-3 py-3">
          <Heatmap days={st.activity ?? []} />
        </div>
      </Panel>

      <div className="grid gap-4 xl:grid-cols-5">
        <Panel label="Latest sessions" className="xl:col-span-3">
          <ul className="divide-y divide-edge">
            {(recent.data ?? []).map((s) => (
              <li key={`${s.agent}/${s.id}`}>
                <Link
                  to="/sessions/$agent/$sessionId"
                  params={{ agent: s.agent, sessionId: s.id }}
                  className="block px-3 py-2 transition-colors hover:bg-surface-2/40"
                >
                  <div className="flex items-baseline gap-2">
                    <AgentDot agent={s.agent} />
                    <span className="truncate text-sm">
                      {s.title || <span className="text-ink-faint">(untitled)</span>}
                    </span>
                    <span className="ml-auto shrink-0 font-mono text-xs text-ok tabular-nums">
                      {fmtCost(s.costUSD, s.unpricedTokens)}
                    </span>
                  </div>
                  <div className="mt-0.5 flex gap-3 font-mono text-[11px] text-ink-faint">
                    <span>{fmtWhen(s.modifiedAt)}</span>
                    <span className="truncate">{shortPath(s.cwd)}</span>
                    <span className="ml-auto shrink-0 tabular-nums">
                      {s.messages} msgs · {fmtTokens(totalTokens(s.tokens))} tok
                    </span>
                  </div>
                </Link>
              </li>
            ))}
          </ul>
          {recent.data?.length === 0 && <EmptyNote>No sessions yet.</EmptyNote>}
        </Panel>

        <div className="space-y-4 xl:col-span-2">
          <Panel label="Agents">
            <table className="w-full text-sm">
              <tbody className="divide-y divide-edge">
                {(st.agents ?? []).map((a) => (
                  <tr key={a.agent}>
                    <td className="px-3 py-1.5">
                      <Link
                        to="/sessions"
                        search={{ agent: a.agent }}
                        className="hover:text-accent"
                      >
                        <AgentChip agent={a.agent} />
                      </Link>
                    </td>
                    <td className="px-2 py-1.5 text-right font-mono text-xs text-ink-dim tabular-nums">
                      {a.sessions} sess
                    </td>
                    <td className="px-2 py-1.5 text-right font-mono text-xs text-ink-dim tabular-nums">
                      {fmtTokens(a.tokens)} tok
                    </td>
                    <td className="px-3 py-1.5 text-right font-mono text-xs text-ok tabular-nums">
                      {fmtCost(a.costUSD)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {(st.agents ?? []).length === 0 && (
              <EmptyNote>No agents detected.</EmptyNote>
            )}
          </Panel>

          <Panel label="Tool calls by kind">
            <KindBars
              items={(st.toolKinds ?? []).map((k) => ({
                label: k.kind,
                count: k.count,
              }))}
              fmt={fmtTokens}
            />
          </Panel>

          <Panel label="Workspaces">
            <ul className="divide-y divide-edge">
              {(st.workspaces ?? []).map((w) => (
                <li key={w.path}>
                  <Link
                    to="/sessions"
                    search={{ project: w.path }}
                    className="flex items-baseline gap-2 px-3 py-1.5 transition-colors hover:bg-surface-2/40"
                  >
                    <span className="truncate font-mono text-xs">
                      {shortPath(w.path)}
                    </span>
                    <span className="ml-auto shrink-0 font-mono text-[11px] text-ink-faint tabular-nums">
                      {w.sessions} · {fmtWhen(w.lastActive ?? "").slice(0, 10)}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          </Panel>
        </div>
      </div>

      <Panel label="Recent file edits">
        <ul className="divide-y divide-edge">
          {(st.recentFiles ?? []).map((f) => (
            <li key={f.path}>
              <Link
                to="/sessions/$agent/$sessionId"
                params={{ agent: f.agent, sessionId: f.sessionId }}
                search={{ tab: "files" }}
                className="flex items-baseline gap-3 px-3 py-1.5 transition-colors hover:bg-surface-2/40"
              >
                <AgentDot agent={f.agent} />
                <span className="truncate font-mono text-xs">
                  {shortPath(f.path)}
                </span>
                <span className="shrink-0 rounded bg-surface-2 px-1.5 font-mono text-[10px] text-ink-faint">
                  {f.kind.replace("file_", "")}
                </span>
                <span className="ml-auto shrink-0 font-mono text-[11px] text-ink-faint">
                  {fmtWhen(f.at ?? "")}
                </span>
              </Link>
            </li>
          ))}
        </ul>
        {(st.recentFiles ?? []).length === 0 && (
          <EmptyNote>No file edits recorded.</EmptyNote>
        )}
      </Panel>
    </div>
  );
}

// Heatmap draws the GitHub-style activity grid as plain SVG: a
// sequential single-hue ramp (accent at four opacity steps — magnitude,
// not identity), native tooltips, no chart library needed.
function Heatmap({ days }: { days: DayActivity[] }) {
  const tooltip = useTooltip();
  const byDay = new Map(days.map((d) => [d.day, d]));
  const CELL = 13;
  const GAP = 3;
  const WEEKS = 52;

  // Grid anchored to the current week's Sunday, going back WEEKS weeks.
  // All calendar math runs in UTC: mixing local Date arithmetic with
  // toISOString (which is UTC) shifts every cell by a day in zones far
  // from UTC, and the activity days from the API are date strings.
  const now = new Date();
  const todayUTC = Date.UTC(
    now.getUTCFullYear(),
    now.getUTCMonth(),
    now.getUTCDate(),
  );
  const DAY_MS = 24 * 60 * 60 * 1000;
  const endUTC = todayUTC + (6 - new Date(todayUTC).getUTCDay()) * DAY_MS;
  const cells: { day: string; week: number; dow: number; d?: DayActivity }[] =
    [];
  for (let w = 0; w < WEEKS; w++) {
    for (let dow = 0; dow < 7; dow++) {
      const t = endUTC - ((WEEKS - 1 - w) * 7 + (6 - dow)) * DAY_MS;
      if (t > todayUTC) continue;
      const day = new Date(t).toISOString().slice(0, 10);
      cells.push({ day, week: w, dow, d: byDay.get(day) });
    }
  }
  const max = Math.max(...days.map((d) => d.sessions), 1);
  // sqrt scaling: one 40-session outlier must not flatten normal days
  // into the faintest step.
  const level = (n: number) =>
    n === 0 ? 0 : Math.min(4, Math.ceil(Math.sqrt(n / max) * 4));
  const FILL = [
    "var(--color-surface-2)",
    "color-mix(in oklab, var(--color-accent) 25%, var(--color-surface-2))",
    "color-mix(in oklab, var(--color-accent) 50%, var(--color-surface-2))",
    "color-mix(in oklab, var(--color-accent) 75%, var(--color-surface-2))",
    "var(--color-accent)",
  ];

  return (
    <>
      <svg
        width={WEEKS * (CELL + GAP)}
        height={7 * (CELL + GAP)}
        role="img"
        aria-label="Daily session activity heatmap"
      >
        {cells.map((c) => (
          <rect
            key={c.day}
            x={c.week * (CELL + GAP)}
            y={c.dow * (CELL + GAP)}
            width={CELL}
            height={CELL}
            rx={2}
            fill={FILL[level(c.d?.sessions ?? 0)]}
            onMouseEnter={(e) =>
              tooltip.show(
                e,
                <>
                  <span className="text-ink">{c.day}</span>
                  <br />
                  {c.d?.sessions ?? 0} session(s)
                  {c.d && c.d.costUSD > 0 && (
                    <>
                      {" · "}
                      <span className="text-ok">{fmtCost(c.d.costUSD)}</span>
                    </>
                  )}
                </>,
              )
            }
            onMouseLeave={tooltip.hide}
          />
        ))}
      </svg>
      {tooltip.node}
    </>
  );
}
