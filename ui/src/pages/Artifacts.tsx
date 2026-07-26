import { useQuery } from "@tanstack/react-query";
import { LoadMore, usePagedList } from "../paged";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { fmtBytes, fmtCount, parityApi, plural } from "../api";
import {
  AgentDot,
  EmptyNote,
  FilterBar,
  groupRuns,
  kindLabel,
  LoadError,
  Loading,
  PageHeader,
  Panel,
  SectionHeading,
  SkeletonRows,
} from "../ui";

const PAGE = 100;

// Every artifact kind the adapters produce, in the order a reader is
// likely to want them: what the agent planned, what it tracked, then the
// environment it captured.
const KIND_ORDER = [
  "plan",
  "todo_list",
  "task_group",
  "memory",
  "paste",
  "shell_snapshot",
  "file_history",
  "usage_facet",
  "usage_report",
];

// What each kind IS. The slugs are adapter vocabulary — "usage_facet" and
// "task_group" mean nothing to someone opening this page for the first
// time, and the old select offered ten of them with no explanation.
const KIND_BLURB: Record<string, string> = {
  plan: "Plans the agent wrote before acting",
  todo_list: "Task lists it tracked while working",
  task_group: "Sub-agent task runs",
  memory: "Long-lived project memory files",
  paste: "Large pastes it stashed out of the transcript",
  shell_snapshot: "Captured shell environments",
  file_history: "Snapshots of files it edited",
  usage_facet: "Raw usage records from the agent",
  usage_report: "Agent-generated usage reports",
};

// Artifact browser: plans, todos, tasks, snapshots, pastes, memories,
// file history, usage data — every sidecar v1 had pages for.
//
// The kinds are FACETS, not a dropdown. A corpus typically holds three or
// four of the ten, and a <select> made the reader open it, read ten
// unfamiliar slugs, and pick one blind to find out which had anything in
// them. The counts come from /stats, so the shape of the corpus is on
// screen before any choice is made.
export function ArtifactsPage() {
  const search = useSearch({ from: "/artifacts" });
  const navigate = useNavigate({ from: "/artifacts" });
  const kind = search.kind ?? "";
  const agent = search.agent ?? "";

  const setFilter = (patch: { agent?: string; kind?: string }) =>
    void navigate({
      search: (prev: { agent?: string; kind?: string }) => ({
        ...prev,
        ...patch,
      }),
      replace: true,
    });

  // Counted under the SAME agent filter as the list. These used to come
  // from corpus-wide /stats, so narrowing to one agent left the rail
  // promising rows the list could not show.
  const facets = useQuery({
    queryKey: ["artifact-kinds", agent],
    queryFn: () => parityApi.artifactKinds(agent),
  });
  const counts = new Map((facets.data ?? []).map((k) => [k.kind, k.count]));
  const kinds = KIND_ORDER.filter((k) => counts.has(k)).concat(
    // Any kind the server knows that this list does not — a new adapter
    // should never silently vanish from the browser.
    [...counts.keys()].filter((k) => !KIND_ORDER.includes(k)),
  );
  const total = [...counts.values()].reduce((a, b) => a + b, 0);

  const {
    rows: artifacts,
    isLoading,
    error,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  } = usePagedList(
    ["artifacts", kind, agent],
    (offset) => parityApi.artifacts(kind, agent, PAGE, offset),
    PAGE,
  );

  // Grouped under kind headings when no kind is chosen: the flat list gave
  // every row a kind badge and let the reader do the grouping by eye.
  //
  // Presented in the rail's order, not the server's alphabetical one — two
  // orderings of the same nine kinds, side by side, is a reading tax for
  // no benefit. Safe to reorder after grouping: the server sorts by kind,
  // so every kind arrives as one contiguous run.
  const rank = (k: string) => {
    const i = KIND_ORDER.indexOf(k);
    return i === -1 ? KIND_ORDER.length : i;
  };
  const groups = groupRuns(artifacts, (a) => a.kind).toSorted(
    (a, b) => rank(a.key) - rank(b.key),
  );

  return (
    <div>
      <PageHeader
        title="Artifacts"
        lede={
          total > 0 && (
            <span className="font-mono text-meta text-ink-faint">
              {plural(total, "artifact")} in {plural(counts.size, "kind")}
            </span>
          )
        }
      >
        <FilterBar agent={agent} onAgent={(v) => setFilter({ agent: v })} />
      </PageHeader>

      <div className="grid gap-4 lg:grid-cols-[13rem_minmax(0,1fr)]">
        <nav
          aria-label="Artifact kinds"
          className="lg:sticky lg:top-5 lg:self-start"
        >
          <Panel label="Kinds">
            <ul className="divide-y divide-edge">
              <KindRow
                label="all kinds"
                count={total}
                active={kind === ""}
                onSelect={() => setFilter({ kind: "" })}
              />
              {kinds.map((k) => (
                <KindRow
                  key={k}
                  label={kindLabel(k)}
                  blurb={KIND_BLURB[k]}
                  count={counts.get(k) ?? 0}
                  active={kind === k}
                  onSelect={() => setFilter({ kind: kind === k ? "" : k })}
                />
              ))}
              {kinds.length === 0 && !facets.isLoading && (
                <li className="px-3 py-4 text-center text-meta text-ink-faint">
                  none indexed
                </li>
              )}
            </ul>
          </Panel>
        </nav>

        <div className="min-w-0">
          {error && <LoadError error={error} />}
          {isLoading && (
            <Loading label="Loading artifacts…">
              <SkeletonRows rows={8} />
            </Loading>
          )}
          {!isLoading && !error && artifacts.length === 0 && (
            <div role="status">
              <EmptyNote
                hint={
                  kind || agent
                    ? "Nothing matches this filter — try another kind, or all agents."
                    : "Artifacts appear here as your agents write plans, todos and memories."
                }
              >
                No artifacts.
              </EmptyNote>
            </div>
          )}

          {groups.length > 0 && (
            <div className="space-y-4">
              {groups.map((g) => (
                <section key={g.key}>
                  <SectionHeading
                    count={
                      counts.has(g.key) && counts.get(g.key) !== g.items.length
                        ? `${fmtCount(g.items.length)} of ${fmtCount(counts.get(g.key) ?? 0)}`
                        : fmtCount(g.items.length)
                    }
                  >
                    {kindLabel(g.key)}
                  </SectionHeading>
                  <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
                    {g.items.map((a) => (
                      <li key={`${a.agent}/${a.kind}/${a.name}`}>
                        <Link
                          to="/artifacts/$agent/$kind/$name"
                          params={{
                            agent: a.agent,
                            kind: a.kind,
                            name: a.name,
                          }}
                          className="flex min-w-0 items-baseline gap-3 bg-surface-1 px-3 py-2 transition-colors hover:bg-surface-2/40"
                        >
                          <AgentDot agent={a.agent} />
                          {/* The name leads. It was third in the row,
                              behind an agent chip and a kind badge that
                              repeated for every row in a filtered list. */}
                          <span
                            className="min-w-0 flex-1 truncate font-mono text-sm"
                            title={a.name}
                          >
                            {a.name}
                          </span>
                          {a.sessions > 0 && (
                            <span className="shrink-0 font-mono text-meta text-ink-faint tabular-nums">
                              {plural(a.sessions, "session")}
                            </span>
                          )}
                          <span className="w-16 shrink-0 text-right font-mono text-meta text-ink-faint tabular-nums">
                            {fmtBytes(a.size)}
                          </span>
                        </Link>
                      </li>
                    ))}
                  </ul>
                </section>
              ))}
            </div>
          )}
          <LoadMore
            hasNextPage={hasNextPage}
            isFetchingNextPage={isFetchingNextPage}
            onLoadMore={fetchNextPage}
          />
        </div>
      </div>
    </div>
  );
}

function KindRow({
  label,
  blurb,
  count,
  active,
  onSelect,
}: {
  label: string;
  blurb?: string;
  count: number;
  active: boolean;
  onSelect: () => void;
}) {
  return (
    <li>
      <button
        type="button"
        aria-pressed={active}
        onClick={onSelect}
        title={blurb}
        className={`flex w-full items-baseline gap-2 border-l-2 px-3 py-1.5 text-left transition-colors ${
          active
            ? "border-accent bg-surface-2/70 text-ink"
            : "border-transparent text-ink-dim hover:bg-surface-2/40 hover:text-ink"
        }`}
      >
        <span className="min-w-0 flex-1 truncate font-mono text-xs">
          {label}
        </span>
        <span className="shrink-0 font-mono text-meta text-ink-faint tabular-nums">
          {fmtCount(count)}
        </span>
      </button>
    </li>
  );
}
