import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { parityApi } from "../api";

export function ArtifactDetailPage() {
  const { agent, kind, name } = useParams({
    from: "/artifacts/$agent/$kind/$name",
  });

  const { data, isLoading, error } = useQuery({
    queryKey: ["artifact", agent, kind, name],
    queryFn: () => parityApi.artifact(agent, kind, name),
  });

  if (isLoading) return <p className="text-ink-dim">Loading…</p>;
  if (error) return <p className="text-warn">{String(error)}</p>;
  const a = data!;

  return (
    <div>
      <div className="mb-1 flex items-baseline gap-3">
        <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-accent">
          {a.kind.replaceAll("_", " ")}
        </span>
        <h1 className="truncate text-xl font-semibold">{a.name}</h1>
      </div>
      <p className="mb-4 text-xs text-ink-dim">
        {a.agent} · {a.size} bytes
      </p>

      {a.sessionIds && a.sessionIds.length > 0 && (
        <div className="mb-4 flex flex-wrap gap-2 text-xs">
          {a.sessionIds.map((sid) => (
            <Link
              key={sid}
              to="/sessions/$agent/$sessionId"
              params={{ agent: a.agent, sessionId: sid }}
              className="rounded-full border border-accent/40 px-2 py-1 text-accent hover:bg-surface-2"
            >
              session {sid.slice(0, 8)}…
            </Link>
          ))}
        </div>
      )}

      {a.contentHTML ? (
        <div
          className="prose-invert max-w-none rounded-lg border border-edge bg-surface-1 p-4 text-sm leading-relaxed [&_a]:text-accent [&_code]:rounded [&_code]:bg-surface-2 [&_code]:px-1 [&_h1]:mb-2 [&_h1]:text-lg [&_h1]:font-semibold [&_h2]:mb-2 [&_h2]:font-semibold [&_li]:ml-4 [&_li]:list-disc [&_p]:mb-2"
          dangerouslySetInnerHTML={{ __html: a.contentHTML }}
        />
      ) : a.content ? (
        <pre className="overflow-x-auto rounded-lg border border-edge bg-surface-1 p-4 text-xs leading-relaxed">
          {a.content}
        </pre>
      ) : (
        <p className="text-ink-dim">(no content)</p>
      )}

      {a.metadata && (
        <details className="mt-4">
          <summary className="cursor-pointer text-sm text-ink-dim">
            Structured metadata
          </summary>
          <pre className="mt-2 overflow-x-auto rounded-lg border border-edge bg-surface-1 p-4 text-xs">
            {prettyJSON(a.metadata)}
          </pre>
        </details>
      )}
    </div>
  );
}

function prettyJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}
