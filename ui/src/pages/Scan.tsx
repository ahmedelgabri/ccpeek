import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { fmtCount, fmtWhen, parityApi, plural, type ScanFinding } from "../api";
import {
  AgentDot,
  EmptyNote,
  LoadError,
  PageHeader,
  Panel,
  SkeletonRows,
  Segmented,
  SkeletonTiles,
} from "../ui";

// Secret-scan findings across EVERY agent's history, with ignore toggles
// persisted as user state (survives rescans and rebuilds).
//
// The page is organised by RULE, not by finding. A scan of a real corpus
// returns the same rule dozens of times — one leaked key pattern matching
// across every session that ever echoed it — and a flat list made that
// read as dozens of unrelated problems. Grouping says what it actually is:
// a handful of distinct issues, each with a number of occurrences, each
// dismissable as a unit.
export function ScanPage() {
  const search = useSearch({ from: "/scan" });
  const navigate = useNavigate({ from: "/scan" });
  const showIgnored = search.ignored ?? false;
  const queryClient = useQueryClient();

  const { data, isLoading, error } = useQuery({
    queryKey: ["scan"],
    queryFn: () => parityApi.scan(true),
  });

  const toggle = useMutation({
    mutationFn: ({ id, ignored }: { id: number; ignored: boolean }) =>
      parityApi.scanIgnore(id, ignored),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["scan"] }),
  });

  const allFindings = useMemo(() => data ?? [], [data]);
  const active = allFindings.filter((f) => !f.ignored).length;
  const ignored = allFindings.length - active;
  const findings = useMemo(
    () => (showIgnored ? allFindings : allFindings.filter((f) => !f.ignored)),
    [allFindings, showIgnored],
  );

  // Grouped by rule, most occurrences first — the order in which they are
  // worth looking at.
  const groups = useMemo(() => {
    const byRule = new Map<string, ScanFinding[]>();
    for (const f of findings) {
      const list = byRule.get(f.ruleId) ?? [];
      list.push(f);
      byRule.set(f.ruleId, list);
    }
    return [...byRule.entries()]
      .map(([ruleId, items]) => ({
        ruleId,
        items,
        description: items[0].description,
        activeCount: items.filter((f) => !f.ignored).length,
      }))
      .toSorted((a, b) => b.activeCount - a.activeCount || b.items.length - a.items.length);
  }, [findings]);

  if (isLoading)
    return (
      <div>
        <PageHeader title="Secret scan" />
        <div role="status" className="space-y-4">
          <span className="sr-only">Loading scan findings…</span>
          <SkeletonTiles n={3} />
          <SkeletonRows rows={6} />
        </div>
      </div>
    );

  return (
    <div>
      <PageHeader
        title="Secret scan"
        lede={
          <span className="font-mono text-meta text-ink-faint">
            {allFindings.length === 0
              ? "nothing detected"
              : `${plural(groups.length, "rule")} · ${plural(allFindings.length, "occurrence")}`}
          </span>
        }
      >
        <Segmented
          label="Which findings to show"
          value={showIgnored ? "all" : "active"}
          onChange={(v) =>
            void navigate({
              search: v === "all" ? { ignored: true } : {},
              replace: true,
            })
          }
          options={[
            { value: "active", label: "active", badge: fmtCount(active) },
            { value: "all", label: "all", badge: fmtCount(allFindings.length) },
          ]}
        />
      </PageHeader>

      {error && <LoadError error={error} />}

      {/* The verdict, stated once and plainly. A page of rows never said
          whether the answer was "you are fine" or "you have a problem". */}
      {!error && (
        <div
          className={`mb-4 rounded-md border px-4 py-3 text-sm ${
            active > 0
              ? "border-warn/50 bg-warn/10"
              : "border-edge bg-surface-1"
          }`}
        >
          {active > 0 ? (
            <p className="text-warn">
              <strong className="font-semibold">
                {plural(active, "active finding")}
              </strong>{" "}
              across {plural(groups.filter((g) => g.activeCount > 0).length, "rule")}.
              Anything already reviewed can be ignored — the decision
              survives rescans.
            </p>
          ) : (
            <p className="text-ink-dim">
              <span className="text-ok">No active findings.</span>{" "}
              {ignored > 0
                ? `${fmtCount(ignored)} ignored.`
                : "Nothing in your agent history matched a secret pattern."}{" "}
              Run <Root>ccpeek scan</Root> to rescan.
            </p>
          )}
        </div>
      )}

      {!error && findings.length === 0 && allFindings.length > 0 && (
        <div role="status">
          <EmptyNote hint="Switch to “all” to review what was dismissed.">
            No active findings · {fmtCount(ignored)} ignored.
          </EmptyNote>
        </div>
      )}

      <div className="space-y-3">
        {groups.map((g) => (
          <RuleGroup
            key={g.ruleId}
            group={g}
            onToggle={(id, ignore) => toggle.mutate({ id, ignored: ignore })}
          />
        ))}
      </div>
    </div>
  );
}

function Root({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded bg-surface-2 px-1 py-0.5 font-mono text-xs text-ink">
      {children}
    </code>
  );
}

interface Group {
  ruleId: string;
  items: ScanFinding[];
  description: string;
  activeCount: number;
}

function RuleGroup({
  group,
  onToggle,
}: {
  group: Group;
  onToggle: (id: number, ignored: boolean) => void;
}) {
  // Big groups start closed: the point of grouping is that you decide per
  // RULE first and only open the one you care about.
  const [open, setOpen] = useState(group.items.length <= 5);
  const allIgnored = group.activeCount === 0;
  return (
    <Panel className={allIgnored ? "opacity-60" : ""}>
      <div className="flex items-baseline gap-3 border-b border-edge px-3 py-2">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="flex min-w-0 flex-1 items-baseline gap-2 text-left"
        >
          <span className="shrink-0 font-mono text-meta text-accent">
            {open ? "▾" : "▸"}
          </span>
          <span
            className={`shrink-0 rounded px-1.5 py-0.5 font-mono text-xs ${
              allIgnored
                ? "bg-surface-2 text-ink-dim"
                : "bg-warn/20 text-warn"
            }`}
          >
            {group.ruleId}
          </span>
          <span className="min-w-0 flex-1 truncate text-sm text-ink-dim">
            {group.description}
          </span>
        </button>
        <span className="shrink-0 font-mono text-meta text-ink-faint tabular-nums">
          {group.activeCount > 0
            ? `${fmtCount(group.activeCount)} active`
            : "all ignored"}
          {group.items.length !== group.activeCount &&
            ` · ${fmtCount(group.items.length)} total`}
        </span>
        {/* One decision for the whole rule. Dismissing the same pattern
            forty times, one row at a time, was the actual cost of a flat
            list. */}
        <button
          type="button"
          onClick={() =>
            group.items
              .filter((f) => f.ignored === allIgnored)
              .forEach((f) => onToggle(f.id, !allIgnored))
          }
          className="shrink-0 rounded-md border border-edge px-2 py-0.5 font-mono text-meta text-ink-dim transition-colors hover:border-edge-strong hover:text-ink"
        >
          {allIgnored ? "restore all" : "ignore all"}
        </button>
      </div>

      {open && (
        <ul className="divide-y divide-edge">
          {group.items.map((f) => (
            <li
              key={f.id}
              className={`flex items-start gap-3 px-3 py-2 ${
                f.ignored ? "opacity-50" : ""
              }`}
            >
              <div className="min-w-0 flex-1">
                {/* The redacted match leads: it is the thing you are
                    deciding about. It used to sit last, under the rule id
                    and a repeated description. */}
                <div className="truncate font-mono text-sm text-ink">
                  {f.matchRedacted}
                </div>
                <FindingLocation finding={f} />
              </div>
              <span className="shrink-0 font-mono text-micro text-ink-faint">
                {fmtWhen(f.scannedAt)}
              </span>
              <button
                type="button"
                onClick={() => onToggle(f.id, !f.ignored)}
                aria-label={`${f.ignored ? "Unignore" : "Ignore"} ${f.ruleId} finding in ${f.naturalKey}`}
                aria-pressed={f.ignored}
                className="shrink-0 rounded-md border border-edge px-2 py-0.5 font-mono text-meta text-ink-dim transition-colors hover:border-edge-strong hover:text-ink"
              >
                {f.ignored ? "unignore" : "ignore"}
              </button>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

// FindingLocation resolves a finding's natural key to the view that holds
// it, so a hit is one click from its context rather than a path to go and
// look up by hand.
function FindingLocation({ finding: f }: { finding: ScanFinding }) {
  const parts = f.naturalKey.split("/");
  if (
    f.entityType === "message" &&
    parts[0] === "message" &&
    parts.length >= 3
  ) {
    const agent = parts[1];
    const sessionId = parts.slice(2).join("/");
    return (
      <Link
        to="/sessions/$agent/$sessionId"
        params={{ agent, sessionId }}
        search={f.line > 0 ? { seq: f.line } : {}}
        className="mt-0.5 flex min-w-0 items-baseline gap-1.5 font-mono text-meta text-ink-faint hover:text-accent"
      >
        <AgentDot agent={agent} />
        <span className="truncate">session {sessionId.slice(0, 8)}</span>
        {f.line > 0 && <span>· message #{f.line}</span>}
        <span className="text-accent">↗</span>
      </Link>
    );
  }
  if (
    f.entityType === "artifact" &&
    parts[0] === "artifact" &&
    parts.length >= 4
  ) {
    const agent = parts[1];
    const kind = parts[2];
    const name = parts.slice(3).join("/");
    return (
      <Link
        to="/artifacts/$agent/$kind/$name"
        params={{ agent, kind, name }}
        className="mt-0.5 flex min-w-0 items-baseline gap-1.5 font-mono text-meta text-ink-faint hover:text-accent"
      >
        <AgentDot agent={agent} />
        <span className="truncate">
          {kind.replaceAll("_", " ")} · {name}
        </span>
        {f.line > 0 && <span>· line {f.line}</span>}
        <span className="text-accent">↗</span>
      </Link>
    );
  }
  return (
    <div className="mt-0.5 truncate font-mono text-meta text-ink-faint">
      {f.entityType} · {f.naturalKey}
      {f.line > 0 && <span> · entry {f.line}</span>}
    </div>
  );
}
