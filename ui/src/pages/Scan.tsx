import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import {
  fmtCount,
  fmtWhen,
  parityApi,
  plural,
  type ScanFinding,
  type ScanRule,
} from "../api";
import {
  AgentDot,
  Code,
  GhostButton,
  kindLabel,
  LoadError,
  Loading,
  PageHeader,
  Panel,
  Segmented,
  SkeletonRows,
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
  // The rule grouping, its counts and its ranking come from the server —
  // this is the reading the product presents, so `ccpeek query scan-rules`
  // and the MCP tool answer with it too rather than the page owning a
  // shape no agent can fetch.
  const rulesQuery = useQuery({
    queryKey: ["scan", "rules"],
    queryFn: () => parityApi.scanRules(),
  });

  // Takes a SET of ids, not one: "ignore all" on a rule is the page's
  // headline affordance, and a leaked-pattern rule realistically has
  // dozens to hundreds of occurrences. One mutation per finding meant that
  // many concurrent POSTs, each invalidating the complete unpaged findings
  // list as it resolved. One await-all, one invalidation.
  const toggle = useMutation({
    mutationFn: async ({
      ids,
      ignored,
    }: {
      ids: number[];
      ignored: boolean;
    }) => {
      await Promise.all(ids.map((id) => parityApi.scanIgnore(id, ignored)));
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["scan"] }),
  });

  const loadError = error ?? rulesQuery.error;
  const allFindings = useMemo(() => data ?? [], [data]);
  const active = allFindings.filter((f) => !f.ignored).length;
  const ignored = allFindings.length - active;
  const findings = useMemo(
    () => (showIgnored ? allFindings : allFindings.filter((f) => !f.ignored)),
    [allFindings, showIgnored],
  );

  // The server ranks the rules; the page only hangs each rule's visible
  // occurrences off it.
  const rules = rulesQuery.data;
  const groups = useMemo(() => {
    const byRule = new Map<string, ScanFinding[]>();
    for (const f of findings) {
      const list = byRule.get(f.ruleId) ?? [];
      list.push(f);
      byRule.set(f.ruleId, list);
    }
    return (rules ?? [])
      .map((rule) => ({ ...rule, items: byRule.get(rule.ruleId) ?? [] }))
      .filter((g) => g.items.length > 0);
  }, [findings, rules]);

  if (isLoading || rulesQuery.isLoading)
    return (
      <div>
        <PageHeader title="Secret scan" />
        <Loading label="Loading scan findings…">
          <SkeletonTiles n={3} />
          <SkeletonRows rows={6} />
        </Loading>
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
              : `${plural(rules?.length ?? 0, "rule")} · ${plural(allFindings.length, "occurrence")}`}
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

      {loadError && <LoadError error={loadError} />}

      {/* The verdict, stated once and plainly. A page of rows never said
          whether the answer was "you are fine" or "you have a problem". */}
      {!loadError && (
        <div
          role="status"
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
              across{" "}
              {plural((rules ?? []).filter((r) => r.active > 0).length, "rule")}
              . Anything already reviewed can be ignored — the decision survives
              rescans.
            </p>
          ) : (
            <p className="text-ink-dim">
              <span className="text-ok">No active findings.</span>{" "}
              {ignored === 0
                ? "Nothing in your agent history matched a secret pattern."
                : showIgnored
                  ? `${fmtCount(ignored)} ignored.`
                  : `${fmtCount(ignored)} ignored — switch to “all” to review what was dismissed.`}{" "}
              Run <Code>ccpeek scan</Code> to rescan.
            </p>
          )}
        </div>
      )}

      <div className="space-y-3">
        {groups.map((g) => (
          <RuleGroup
            key={g.ruleId}
            group={g}
            onToggle={(ids, ignore) => toggle.mutate({ ids, ignored: ignore })}
          />
        ))}
      </div>
    </div>
  );
}

// A server-ranked rule plus the occurrences currently in view — which is
// fewer than `findings` whenever ignored ones are hidden, so the counts in
// the header come from the rule, not from the list.
type Group = ScanRule & { items: ScanFinding[] };

function RuleGroup({
  group,
  onToggle,
}: {
  group: Group;
  onToggle: (ids: number[], ignored: boolean) => void;
}) {
  // Big groups start closed: the point of grouping is that you decide per
  // RULE first and only open the one you care about.
  const [open, setOpen] = useState(group.items.length <= 5);
  const allIgnored = group.active === 0;
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
              allIgnored ? "bg-surface-2 text-ink-dim" : "bg-warn/20 text-warn"
            }`}
          >
            {group.ruleId}
          </span>
          <span className="min-w-0 flex-1 truncate text-sm text-ink-dim">
            {group.description}
          </span>
        </button>
        <span className="shrink-0 font-mono text-meta text-ink-faint tabular-nums">
          {group.active > 0
            ? `${fmtCount(group.active)} active`
            : "all ignored"}
          {group.findings !== group.active &&
            ` · ${fmtCount(group.findings)} total`}
        </span>
        {/* One decision for the whole rule. Dismissing the same pattern
            forty times, one row at a time, was the actual cost of a flat
            list. */}
        <GhostButton
          onClick={() =>
            onToggle(
              group.items
                .filter((f) => f.ignored === allIgnored)
                .map((f) => f.id),
              !allIgnored,
            )
          }
        >
          {allIgnored ? "restore all" : "ignore all"}
        </GhostButton>
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
              <GhostButton
                onClick={() => onToggle([f.id], !f.ignored)}
                aria-label={`${f.ignored ? "Unignore" : "Ignore"} ${f.ruleId} finding in ${f.naturalKey}`}
                aria-pressed={f.ignored}
              >
                {f.ignored ? "unignore" : "ignore"}
              </GhostButton>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

const LOCATION_CLS =
  "mt-0.5 flex min-w-0 items-baseline gap-1.5 font-mono text-meta text-ink-faint hover:text-accent";

// Both destinations read the same way — agent mark, what it is, where in
// it, and the jump arrow — so only the words differ between them.
function LocationBody({
  agent,
  label,
  detail,
}: {
  agent: string;
  label: string;
  detail?: string;
}) {
  return (
    <>
      <AgentDot agent={agent} />
      <span className="truncate">{label}</span>
      {detail && <span>{detail}</span>}
      <span className="text-accent">↗</span>
    </>
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
        className={LOCATION_CLS}
      >
        <LocationBody
          agent={agent}
          label={`session ${sessionId.slice(0, 8)}`}
          detail={f.line > 0 ? `· message #${f.line}` : undefined}
        />
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
        className={LOCATION_CLS}
      >
        <LocationBody
          agent={agent}
          label={`${kindLabel(kind)} · ${name}`}
          detail={f.line > 0 ? `· line ${f.line}` : undefined}
        />
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
