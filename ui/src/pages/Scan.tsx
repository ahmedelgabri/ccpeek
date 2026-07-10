import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { parityApi } from "../api";

// Secret-scan findings across EVERY agent's history, with ignore toggles
// persisted as user state (survives rescans and rebuilds).
export function ScanPage() {
  const [showIgnored, setShowIgnored] = useState(false);
  const queryClient = useQueryClient();

  const { data, isLoading, error } = useQuery({
    queryKey: ["scan", showIgnored],
    queryFn: () => parityApi.scan(showIgnored),
  });

  const toggle = useMutation({
    mutationFn: ({ id, ignored }: { id: number; ignored: boolean }) =>
      parityApi.scanIgnore(id, ignored),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["scan"] }),
  });

  const findings = data ?? [];
  const active = findings.filter((f) => !f.ignored).length;

  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <h1 className="text-xl font-semibold">Secret scan</h1>
        {active > 0 && (
          <span className="rounded-full bg-warn/20 px-2 py-0.5 text-xs text-warn">
            {active} active
          </span>
        )}
        <label className="ml-auto flex items-center gap-2 text-sm text-ink-dim">
          <input
            type="checkbox"
            checked={showIgnored}
            onChange={(e) => setShowIgnored(e.target.checked)}
          />
          show ignored
        </label>
      </div>

      {error && <p className="text-warn">Failed to load: {String(error)}</p>}
      {isLoading && <p className="text-ink-dim">Loading…</p>}
      {!isLoading && findings.length === 0 && (
        <p className="text-ok">No secrets detected. Run `ccpeek scan --v2` to rescan.</p>
      )}

      <ul className="divide-y divide-edge overflow-hidden rounded-lg border border-edge">
        {findings.map((f) => (
          <li
            key={f.id}
            className={`flex items-center gap-3 bg-surface-1 px-4 py-3 ${
              f.ignored ? "opacity-50" : ""
            }`}
          >
            <span className="rounded bg-warn/20 px-1.5 py-0.5 font-mono text-xs text-warn">
              {f.ruleId}
            </span>
            <div className="min-w-0">
              <div className="truncate text-sm">
                {f.naturalKey}
                {f.line > 0 && <span className="text-ink-dim"> · entry {f.line}</span>}
              </div>
              <div className="font-mono text-xs text-ink-dim">{f.matchRedacted}</div>
            </div>
            <button
              onClick={() => toggle.mutate({ id: f.id, ignored: !f.ignored })}
              className="ml-auto shrink-0 rounded-md border border-edge px-2 py-1 text-xs text-ink-dim hover:text-ink"
            >
              {f.ignored ? "unignore" : "ignore"}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
