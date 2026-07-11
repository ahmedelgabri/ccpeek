import { useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { parityApi } from "../api";
import { useHighlight } from "../highlight";
import { SkeletonRows } from "../ui";

export function ArtifactDetailPage() {
  const { agent, kind, name } = useParams({
    from: "/artifacts/$agent/$kind/$name",
  });

  const { data, isLoading, error } = useQuery({
    queryKey: ["artifact", agent, kind, name],
    queryFn: () => parityApi.artifact(agent, kind, name),
  });
  const body = useRef<HTMLDivElement>(null);
  useHighlight(body, [data]);

  if (isLoading) return <SkeletonRows rows={5} />;
  if (error) return <p className="text-warn">{String(error)}</p>;
  const a = data!;

  const rawURL = `/api/v1/artifacts/${agent}/${kind}/${encodeURIComponent(name)}/raw`;

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

      {a.kind === "usage_report" ? (
        // Agent-produced HTML renders isolated in a sandboxed iframe (no
        // same-origin access), exactly like v1 hosted the usage report.
        <iframe
          src={rawURL}
          sandbox="allow-scripts"
          title={`${a.name} (usage report)`}
          className="h-[75vh] w-full rounded-lg border border-edge bg-white"
        />
      ) : a.contentHTML ? (
        <div
          ref={body}
          className="prose-msg max-w-none rounded-lg border border-edge bg-surface-1 p-4"
          dangerouslySetInnerHTML={{ __html: a.contentHTML }}
        />
      ) : a.content ? (
        <div ref={body}>
          <pre className="overflow-x-auto rounded-lg border border-edge bg-surface-1 p-4 text-xs leading-relaxed">
            <code className={a.kind === "shell_snapshot" ? "language-bash" : ""}>
              {a.content}
            </code>
          </pre>
        </div>
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
