import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { fmtCount, fmtWhen, parityApi, plural, type ScanFinding } from "../api";
import { EmptyNote, LoadError, PageHeader, SkeletonRows } from "../ui";

// Secret-scan findings across EVERY agent's history, with ignore toggles
// persisted as user state (survives rescans and rebuilds).
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

  const allFindings = data ?? [];
  const active = allFindings.filter((f) => !f.ignored).length;
  const ignored = allFindings.length - active;
  const findings = showIgnored
    ? allFindings
    : allFindings.filter((f) => !f.ignored);

  return (
    <div>
      <PageHeader
        title="Secret scan"
        lede={
          active > 0 ? (
            <span className="rounded-full bg-warn/20 px-2 py-0.5 text-xs text-warn">
              {plural(active, "active finding")}
            </span>
          ) : (
            <span className="font-mono text-meta text-ink-faint">
              nothing active{ignored > 0 ? ` · ${fmtCount(ignored)} ignored` : ""}
            </span>
          )
        }
      >
        <button
          type="button"
          aria-pressed={showIgnored}
          onClick={() =>
            void navigate({
              search: showIgnored ? {} : { ignored: true },
              replace: true,
            })
          }
          className={`ml-auto rounded-md border border-edge px-2 py-1.5 font-mono text-xs transition-colors hover:border-edge-strong ${
            showIgnored ? "bg-surface-2 text-ink" : "text-ink-dim"
          }`}
        >
          show ignored
        </button>
      </PageHeader>

      {error && <LoadError error={error} />}
      {isLoading && (
        <div role="status">
          <span className="sr-only">Loading scan findings…</span>
          <SkeletonRows rows={6} />
        </div>
      )}
      {!isLoading && !error && findings.length === 0 && (
        <div role="status">
          <EmptyNote>
            {allFindings.length === 0 ? (
              <>
                <span className="text-ok">No secrets detected.</span> Run{" "}
                <code className="font-mono text-ink">ccpeek scan</code> to
                rescan.
              </>
            ) : (
              <>No active findings · {ignored} ignored.</>
            )}
          </EmptyNote>
        </div>
      )}

      {findings.length > 0 && (
        <ul className="divide-y divide-edge overflow-hidden rounded-md border border-edge">
          {findings.map((f) => (
            <li
              key={f.id}
              className={`flex items-start gap-3 bg-surface-1 px-4 py-3 ${
                f.ignored ? "opacity-50" : ""
              }`}
            >
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span
                    title={f.description}
                    className="shrink-0 rounded bg-warn/20 px-1.5 py-0.5 font-mono text-xs text-warn"
                  >
                    {f.ruleId}
                  </span>
                  <span className="shrink-0 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-meta text-ink-dim">
                    {f.entityType}
                  </span>
                  <span className="min-w-0 truncate text-sm">
                    {f.description}
                  </span>
                  <span className="ml-auto shrink-0 font-mono text-meta text-ink-faint">
                    {fmtWhen(f.scannedAt)}
                  </span>
                </div>
                <FindingLocation finding={f} />
                <div className="truncate font-mono text-xs text-ink-dim">
                  {f.matchRedacted}
                </div>
              </div>
              <button
                onClick={() => toggle.mutate({ id: f.id, ignored: !f.ignored })}
                aria-label={`${f.ignored ? "Unignore" : "Ignore"} ${f.ruleId} finding in ${f.naturalKey}`}
                aria-pressed={f.ignored}
                className="shrink-0 rounded-md border border-edge px-2 py-1 text-xs text-ink-dim transition-colors hover:border-edge-strong hover:text-ink"
              >
                {f.ignored ? "unignore" : "ignore"}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

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
        className="mt-1 block truncate text-sm text-accent hover:underline"
      >
        {f.naturalKey}
        {f.line > 0 && (
          <span className="text-ink-dim"> · message #{f.line}</span>
        )}
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
        className="mt-1 block truncate text-sm text-accent hover:underline"
      >
        {f.naturalKey}
        {f.line > 0 && <span className="text-ink-dim"> · line {f.line}</span>}
      </Link>
    );
  }
  return (
    <div className="mt-1 truncate text-sm">
      {f.naturalKey}
      {f.line > 0 && <span className="text-ink-dim"> · entry {f.line}</span>}
    </div>
  );
}
