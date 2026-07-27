import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  api,
  clipPath,
  fmtCost,
  fmtCount,
  fmtTokens,
  parityApi,
  plural,
  shortPath,
  totalTokens,
  type DayActivity,
} from "../api";
import { fmtWhen, fullWhen } from "../time";
import {
  AgentChip,
  AgentDot,
  BudgetMeter,
  budgetVerdict,
  Code,
  EmptyNote,
  KindBars,
  Loading,
  Money,
  PageHeader,
  Panel,
  SkeletonRows,
  SkeletonTiles,
  Sparkline,
  StatTile,
  useTooltip,
} from "../ui";

// Overview is the instrument panel: headline counters, the activity
// heatmap, spend against budget, and the relation facets (agents,
// workspaces, latest sessions, latest file edits) — every figure links
// into its detail view.
export function OverviewPage() {
  const stats = useQuery({ queryKey: ["stats"], queryFn: api.stats });
  const recent = useQuery({
    queryKey: ["sessions", "recent"],
    queryFn: () => api.sessions({ limit: "12" }),
  });
  // Workspaces ranked by spend, not by session count: "where does the
  // money go" is the question this panel is on the page to answer, and a
  // count answers a different one. /stats has no cost per workspace, so
  // the project rollup — the same source Usage uses — supplies it.
  // Limited: the panel shows eight, and the server already orders project
  // groups by cost desc. Unlimited, this fetched every workspace ever
  // indexed to display eight of them. Nine because at most one group is
  // the blank "no workspace" bucket the panel filters out.
  const byProject = useQuery({
    queryKey: ["usage", "project"],
    queryFn: () => api.usage({ group: "project", limit: "9" }),
  });
  const budget = useQuery({ queryKey: ["budget"], queryFn: parityApi.budget });

  const st = stats.data;
  if (stats.isLoading)
    return (
      <Loading label="Loading overview…">
        <SkeletonTiles />
        <SkeletonRows rows={8} />
      </Loading>
    );
  if (!st) return <EmptyNote>No data indexed yet.</EmptyNote>;

  // Nothing indexed at all is a first run, not an empty dashboard. Six
  // zero counters and an empty grid told the user nothing about WHY, and
  // gave them nowhere to go — which is exactly the moment agent-root
  // detection has failed and they need to know it.
  if (st.sessions === 0) return <FirstRun />;

  const last30 = (st.activity ?? []).slice(-30);

  return (
    <div className="space-y-4">
      <PageHeader
        title="Overview"
        lede={
          <span className="font-mono text-meta text-ink-faint">
            all time · {plural(st.sessions, "session")} across{" "}
            {plural((st.agents ?? []).length, "agent")}
          </span>
        }
      />

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
        <StatTile
          label="Sessions"
          value={fmtCount(st.sessions)}
          to="/sessions"
          spark={<Sparkline values={last30.map((d) => d.sessions)} />}
        />
        <StatTile
          label="Cost / month"
          value={<Money usd={st.costMonthUSD} />}
          detail={`${fmtCost(st.costUSD)} all time`}
          to="/usage"
          spark={<Sparkline values={last30.map((d) => d.costUSD)} />}
        />
        <StatTile
          label="Tokens"
          value={fmtTokens(st.tokens)}
          detail={`${fmtCount(st.messages)} messages`}
          to="/usage"
        />
        <StatTile
          label="Commands"
          value={fmtCount(st.commands)}
          detail={`${fmtCount(st.toolCalls)} tool calls`}
          to="/commands"
        />
        <StatTile
          label="Artifacts"
          value={fmtCount(st.artifacts)}
          to="/artifacts"
        />
        <StatTile
          label="Scan findings"
          value={fmtCount(st.scanFindings)}
          tone={st.scanFindings > 0 ? "warn" : undefined}
          to="/scan"
        />
      </div>

      <div className="grid gap-4 xl:grid-cols-5">
        {/* Labelled UTC because it is: the API's activity days are UTC
            date strings and the grid's calendar math matches them. Every
            other time in the UI is local, so the one surface that is not
            has to say so. */}
        <Panel
          label="Activity — sessions per UTC day, last 52 weeks"
          className="xl:col-span-3"
        >
          <Heatmap days={st.activity ?? []} />
        </Panel>
        <BudgetPace className="xl:col-span-2" budget={budget.data} />
      </div>

      <div className="grid gap-4 xl:grid-cols-5">
        <Panel label="Latest sessions" className="xl:col-span-3">
          {(recent.data ?? []).length === 0 ? (
            <EmptyNote>No sessions yet.</EmptyNote>
          ) : (
            <ul className="divide-y divide-edge">
              {(recent.data ?? []).map((s) => (
                <li key={`${s.agent}/${s.id}`}>
                  <Link
                    to="/sessions/$agent/$sessionId"
                    params={{ agent: s.agent, sessionId: s.id }}
                    className="block px-3 py-2 transition-colors hover:bg-surface-2/40"
                  >
                    {/* min-w-0 on the flex child is what lets `truncate`
                        actually truncate. Without it the title kept its
                        full intrinsic width and pushed the page's
                        min-content out to 1222px — a horizontal scrollbar
                        on every viewport below xl (854px of overflow at
                        390px wide). */}
                    <div className="flex min-w-0 items-baseline gap-2">
                      <AgentDot agent={s.agent} />
                      <span className="min-w-0 flex-1 truncate text-sm">
                        {s.title || (
                          <span className="text-ink-faint">(untitled)</span>
                        )}
                      </span>
                      <Money
                        usd={s.costUSD}
                        unpriced={s.unpricedTokens}
                        className="shrink-0 text-xs"
                      />
                    </div>
                    <div className="mt-0.5 flex min-w-0 gap-3 font-mono text-meta text-ink-faint">
                      <span
                        title={fullWhen(s.modifiedAt)}
                        className="shrink-0"
                      >
                        {fmtWhen(s.modifiedAt)}
                      </span>
                      <span className="min-w-0 flex-1 truncate">
                        {shortPath(s.cwd)}
                      </span>
                      <span className="shrink-0 tabular-nums">
                        {fmtCount(s.messages)} msgs ·{" "}
                        {fmtTokens(totalTokens(s.tokens))} tok
                      </span>
                    </div>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Panel>

        <div className="min-w-0 space-y-4 xl:col-span-2">
          <Panel label="Agents">
            {(st.agents ?? []).length === 0 ? (
              <EmptyNote>No agents detected.</EmptyNote>
            ) : (
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
                        {fmtCount(a.sessions)} sess
                      </td>
                      <td className="px-2 py-1.5 text-right font-mono text-xs text-ink-dim tabular-nums">
                        {fmtTokens(a.tokens)} tok
                      </td>
                      <td className="px-3 py-1.5 text-right">
                        <Money usd={a.costUSD} className="text-xs" />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>

          <Panel label="Tool calls by kind">
            <KindBars
              items={(st.toolKinds ?? []).map((k) => ({
                label: k.kind,
                count: k.count,
              }))}
              fmt={fmtCount}
            />
          </Panel>

          <Panel label="Workspaces by cost">
            <WorkspacesByCost
              rows={byProject.data ?? []}
              loading={byProject.isLoading}
            />
          </Panel>

          {/* Demoted from a full-width panel at the foot of the page: file
              edits read as noise without their session around them, and
              the session's own Files tab is where they have that
              context. The newest few stay here as a way in. */}
          <Panel label="Recent file edits">
            {(st.recentFiles ?? []).length === 0 ? (
              <EmptyNote>No file edits recorded.</EmptyNote>
            ) : (
              <ul className="divide-y divide-edge">
                {(st.recentFiles ?? []).slice(0, 8).map((f) => (
                  <li key={`${f.agent}/${f.sessionId}/${f.path}/${f.at}`}>
                    <Link
                      to="/sessions/$agent/$sessionId"
                      params={{ agent: f.agent, sessionId: f.sessionId }}
                      search={{ tab: "files" }}
                      className="flex min-w-0 items-baseline gap-2 px-3 py-1.5 transition-colors hover:bg-surface-2/40"
                    >
                      <AgentDot agent={f.agent} />
                      {/* Left-clipped: a file path differentiates at its
                          tail, so cutting the end would leave a column of
                          identical "internal/handle…" rows. */}
                      <span
                        className="min-w-0 flex-1 truncate font-mono text-xs"
                        title={shortPath(f.path)}
                      >
                        {clipPath(f.path, 34)}
                      </span>
                      <span className="shrink-0 rounded bg-surface-2 px-1.5 font-mono text-micro text-ink-faint">
                        {f.kind.replace("file_", "")}
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </Panel>
        </div>
      </div>
    </div>
  );
}

// FirstRun replaces the dashboard when the index is empty: it says where
// ccpeek looked and what to do about it, instead of showing six zeroes.
function FirstRun() {
  return (
    <div>
      <PageHeader title="Overview" />
      <Panel label="Nothing indexed yet">
        <div className="max-w-prose space-y-3 px-4 py-5 text-sm text-ink-dim">
          <p className="text-ink">
            ccpeek did not find any coding-agent history to index.
          </p>
          <p>
            It looks for Claude Code in <Code>~/.claude</Code>, Pi in{" "}
            <Code>~/.pi/agent</Code>, Codex CLI in <Code>~/.codex</Code>,
            OpenCode in <Code>~/.local/share/opencode</Code>, and Cursor in{" "}
            <Code>~/.cursor</Code>.
          </p>
          <p>
            If your history lives elsewhere, point ccpeek at it with{" "}
            <Code>--claude-dir</Code> or the matching environment variable (
            <Code>PI_CODING_AGENT_DIR</Code>, <Code>CODEX_HOME</Code>,{" "}
            <Code>OPENCODE_DATA_DIR</Code>, <Code>CCPEEK_CURSOR_DIR</Code>),
            then restart with <Code>--rebuild</Code>.
          </p>
          <p className="text-ink-faint">
            Indexing runs in the background — if it is still working, this page
            fills in on its own.
          </p>
        </div>
      </Panel>
    </div>
  );
}

// BudgetPace answers "am I on track", which a bare month-to-date figure
// cannot: it compares spend against the share of the month elapsed.
function BudgetPace({
  budget,
  className = "",
}: {
  budget: import("../api").Budget | undefined;
  className?: string;
}) {
  if (!budget) return null;
  // The projection and its verdict come from the query layer, so `ccpeek
  // query budget` and the MCP tool answer "am I on track" too — this used
  // to be month arithmetic living in a React component.
  const {
    spentUSD: spentMonth,
    monthlyUSD: monthly,
    month,
    dayOfMonth,
    daysInMonth,
    projectedUSD: projected,
    pace,
  } = budget;

  if (monthly <= 0) {
    return (
      <Panel label="Spend this month" className={className}>
        <div className="space-y-2 px-3 py-3">
          <div className="flex items-baseline gap-2">
            <Money usd={spentMonth} className="text-xl" />
            <span className="font-mono text-meta text-ink-faint">
              day {dayOfMonth} of {daysInMonth}
            </span>
          </div>
          <p className="text-meta text-ink-faint">
            On this pace, {fmtCost(projected)} by month end.
          </p>
          <Link
            to="/usage"
            className="inline-block font-mono text-meta text-accent hover:underline"
          >
            set a monthly budget →
          </Link>
        </div>
      </Panel>
    );
  }

  const warn = pace === "over" || pace === "fast";
  return (
    <Panel label={`Budget — ${month || "this month"}`} className={className}>
      <div className="space-y-2 px-3 py-3">
        <BudgetMeter
          budget={budget}
          label={
            <>
              <Money usd={spentMonth} className="text-xl" />
              <span className="font-mono text-meta text-ink-faint">
                of {fmtCost(monthly)}
              </span>
            </>
          }
        />
        <p className={`text-meta ${warn ? "text-warn" : "text-ink-faint"}`}>
          {budgetVerdict(budget)}
        </p>
      </div>
    </Panel>
  );
}

function WorkspacesByCost({
  rows,
  loading,
}: {
  rows: import("../api").UsageRow[];
  loading: boolean;
}) {
  if (loading)
    return <p className="px-3 py-3 text-meta text-ink-faint">Loading…</p>;
  // Server order is already cost desc; only the blank bucket is dropped.
  const top = rows.filter((r) => r.group).slice(0, 8);
  if (top.length === 0) return <EmptyNote>No workspaces recorded.</EmptyNote>;
  const max = Math.max(...top.map((r) => r.costUSD), Number.EPSILON);
  return (
    <ul className="divide-y divide-edge">
      {top.map((r) => (
        <li key={r.group}>
          <Link
            to="/sessions"
            search={{ project: r.group }}
            className="flex min-w-0 items-baseline gap-2 px-3 py-1.5 transition-colors hover:bg-surface-2/40"
          >
            <span className="min-w-0 flex-1 truncate font-mono text-xs">
              {shortPath(r.group)}
            </span>
            <span
              aria-hidden
              className="hidden h-1.5 w-16 shrink-0 overflow-hidden rounded-sm bg-surface-2 sm:block"
            >
              <span
                className="block h-full rounded-sm bg-accent/70"
                style={{ width: `${Math.max((r.costUSD / max) * 100, 2)}%` }}
              />
            </span>
            <Money usd={r.costUSD} className="shrink-0 text-meta" />
          </Link>
        </li>
      ))}
    </ul>
  );
}

// Heatmap draws the GitHub-style activity grid as plain SVG: a
// sequential single-hue ramp (accent at four opacity steps — magnitude,
// not identity), no chart library needed.
//
// Days with activity are LINKS to that day's sessions. That is what makes
// the grid keyboard-reachable and screen-reader-legible — 364 mouse-only
// <rect>s exposed their figures to pointer users and nobody else — and it
// turns the most obvious gesture on a heatmap, "what happened there", into
// something the grid can actually answer.
const CELL = 13;
const GAP = 3;
const WEEKS = 52;
const DAY_MS = 24 * 60 * 60 * 1000;
const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];
const FILL = [
  "var(--color-surface-2)",
  "color-mix(in oklab, var(--color-accent) 25%, var(--color-surface-2))",
  "color-mix(in oklab, var(--color-accent) 50%, var(--color-surface-2))",
  "color-mix(in oklab, var(--color-accent) 75%, var(--color-surface-2))",
  "var(--color-accent)",
];

function Heatmap({ days }: { days: DayActivity[] }) {
  const tooltip = useTooltip();
  const byDay = new Map(days.map((d) => [d.day, d]));

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

  // A month label sits above the first week whose Sunday starts a new
  // month — without them a 52-column grid gives the reader no way to
  // place any column in the year.
  const monthLabels: { week: number; label: string }[] = [];
  let lastMonth = -1;
  for (let w = 0; w < WEEKS; w++) {
    const t = endUTC - (WEEKS - 1 - w) * 7 * DAY_MS - 6 * DAY_MS;
    const m = new Date(t).getUTCMonth();
    if (m === lastMonth) continue;
    lastMonth = m;
    // A month whose first Sunday lands within three columns of the
    // previous label is dropped: the two would overprint each other
    // ("JulAug") rather than labelling anything.
    const prev = monthLabels[monthLabels.length - 1];
    if (prev && w - prev.week < 3) continue;
    monthLabels.push({ week: w, label: MONTHS[m] });
  }

  const GUTTER = 26;
  const HEADER = 14;
  const width = GUTTER + WEEKS * (CELL + GAP);
  const height = HEADER + 7 * (CELL + GAP);

  return (
    <div className="px-3 py-3">
      <div className="overflow-x-auto">
        <svg
          width={width}
          height={height}
          role="group"
          aria-label="Daily session activity, last 52 weeks"
        >
          {monthLabels.map((m) => (
            <text
              key={`${m.week}-${m.label}`}
              x={GUTTER + m.week * (CELL + GAP)}
              y={10}
              className="fill-ink-faint font-mono"
              fontSize={9}
            >
              {m.label}
            </text>
          ))}
          {[
            { dow: 1, label: "Mon" },
            { dow: 3, label: "Wed" },
            { dow: 5, label: "Fri" },
          ].map((d) => (
            <text
              key={d.label}
              x={0}
              y={HEADER + d.dow * (CELL + GAP) + CELL - 2}
              className="fill-ink-faint font-mono"
              fontSize={9}
            >
              {d.label}
            </text>
          ))}
          {cells.map((c) => {
            const n = c.d?.sessions ?? 0;
            const label = `${c.day}: ${plural(n, "session")}${
              c.d && c.d.costUSD > 0 ? `, ${fmtCost(c.d.costUSD)}` : ""
            }`;
            const rect = (
              <rect
                x={GUTTER + c.week * (CELL + GAP)}
                y={HEADER + c.dow * (CELL + GAP)}
                width={CELL}
                height={CELL}
                rx={2}
                fill={FILL[level(n)]}
              />
            );
            const tip = (
              <>
                <span className="text-ink">{c.day}</span>
                <span className="text-ink-faint"> UTC</span>
                <br />
                {plural(n, "session")}
                {c.d && c.d.costUSD > 0 && (
                  <>
                    {" · "}
                    <span className="text-ink">{fmtCost(c.d.costUSD)}</span>
                  </>
                )}
              </>
            );
            // Empty days are inert: they carry no figure worth a tab stop,
            // and making all 364 focusable would bury the keyboard user.
            if (n === 0) return <g key={c.day}>{rect}</g>;
            return (
              <Link
                key={c.day}
                to="/sessions"
                search={{ since: c.day, until: c.day }}
                aria-label={label}
                className="outline-offset-2"
                {...tooltip.bind(tip)}
              >
                {rect}
              </Link>
            );
          })}
        </svg>
      </div>
      <div className="mt-2 flex items-center gap-1.5 font-mono text-micro text-ink-faint">
        <span className="mr-auto">
          {plural(
            days.reduce((a, d) => a + d.sessions, 0),
            "session",
          )}{" "}
          in the last year
        </span>
        <span>less</span>
        {FILL.map((f, i) => (
          <span
            key={i}
            aria-hidden
            className="inline-block h-2.5 w-2.5 rounded-[2px]"
            style={{ background: f }}
          />
        ))}
        <span>more</span>
      </div>
      {tooltip.node}
    </div>
  );
}
