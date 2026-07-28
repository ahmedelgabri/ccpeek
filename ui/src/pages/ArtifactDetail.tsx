import { useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { fmtBytes, parityApi } from "../api";
import { useHighlight } from "../highlight";
import {
  AgentChip,
  CopyButton,
  kindLabel,
  LoadError,
  Loading,
  SkeletonRows,
} from "../ui";

export function ArtifactDetailPage() {
  const { agent, kind, name } = useParams({
    from: "/artifacts/$agent/$kind/$name",
  });
  const navigate = useNavigate();

  const { data, isLoading, error } = useQuery({
    queryKey: ["artifact", agent, kind, name],
    queryFn: () => parityApi.artifact(agent, kind, name),
  });
  const body = useRef<HTMLDivElement>(null);
  useHighlight(body, [data]);

  // Memory files cross-link with relative markdown links
  // ("[Title](sibling.md)"), but artifact names carry a directory prefix
  // ("-Users-x-proj/MEMORY.md") the bare href loses — resolve clicks
  // against the current artifact's prefix instead of the SPA URL.
  const onContentClick = (e: React.MouseEvent) => {
    if (!(e.target instanceof Element)) return;
    const anchor = e.target.closest("a");
    if (!anchor) return;
    const href = anchor.getAttribute("href") ?? "";
    if (href === "" || /^([a-z][a-z0-9+.-]*:|\/|#)/i.test(href)) return;
    e.preventDefault();
    const dir = name.includes("/")
      ? name.slice(0, name.lastIndexOf("/") + 1)
      : "";
    const sibling = dir + decodeURIComponent(href).replace(/^\.\//, "");
    void navigate({
      to: "/artifacts/$agent/$kind/$name",
      params: { agent, kind, name: sibling },
    });
  };

  if (isLoading)
    return (
      <Loading label="Loading artifact…">
        <SkeletonRows rows={5} />
      </Loading>
    );
  if (error) return <LoadError error={error} />;
  const a = data!;

  const rawURL = `/api/v1/artifacts/${agent}/${kind}/${encodeURIComponent(name)}/raw`;

  return (
    <div>
      <div className="mb-1 flex items-baseline gap-3">
        <Link
          to="/artifacts"
          search={{ kind: a.kind }}
          className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-accent hover:bg-surface-2/70"
        >
          {kindLabel(a.kind)}
        </Link>
        <h1 className="truncate text-xl font-semibold">{a.name}</h1>
      </div>
      <div className="mb-4 flex items-center gap-2 text-xs text-ink-dim">
        <AgentChip agent={a.agent} />
        <span>·</span>
        <span>{fmtBytes(a.size)}</span>
        <span>·</span>
        <a href={rawURL} className="font-mono text-accent hover:underline">
          raw
        </a>
        {a.content && <CopyButton text={a.content} />}
      </div>

      {a.sessionIds && a.sessionIds.length > 0 && (
        <div className="mb-4 flex flex-wrap gap-2 text-xs">
          {a.sessionIds.map((sid) => {
            // When the producing tool call is known, deep-link straight to
            // that message; otherwise the pill opens the session at the top.
            const seq = a.sessionAnchors?.[sid];
            return (
              <Link
                key={sid}
                to="/sessions/$agent/$sessionId"
                params={{ agent: a.agent, sessionId: sid }}
                search={seq !== undefined ? { seq } : {}}
                className="rounded-full border border-accent/40 px-2 py-1 text-accent hover:bg-surface-2"
              >
                session {sid.slice(0, 8)}…
                {seq !== undefined && (
                  <span className="text-ink-faint"> · ↗ #{seq}</span>
                )}
              </Link>
            );
          })}
        </div>
      )}

      {a.kind === "usage_report" ? (
        // Agent-produced HTML renders isolated in a sandboxed iframe (no
        // same-origin access), exactly like v1 hosted the usage report.
        //
        // The canvas behind it is DELIBERATELY light: these reports carry
        // their own stylesheets, written for a white page and usually
        // setting dark text on no background at all — so an app-colored
        // surface underneath would render them dark-on-dark. It follows the
        // theme only as far as it can: a flat #fff sheet glares out of a
        // dark page, so dark mode gets a dimmed off-white that the report's
        // own colors still read against.
        <figure>
          <iframe
            src={rawURL}
            sandbox="allow-scripts"
            title={`${a.name} (usage report)`}
            style={{ background: "light-dark(#ffffff, #e8e6e1)" }}
            className="h-[75vh] w-full rounded-md border border-edge"
          />
          <figcaption className="mt-1.5 font-mono text-micro text-ink-faint">
            The agent's own HTML report, sandboxed and rendered on a light
            canvas it was written for.
          </figcaption>
        </figure>
      ) : a.contentHTML ? (
        <div
          ref={body}
          onClick={onContentClick}
          className="prose-msg prose-measure rounded-md border border-edge bg-surface-1 p-4"
          dangerouslySetInnerHTML={{ __html: a.contentHTML }}
        />
      ) : a.content ? (
        <div ref={body}>
          <pre className="overflow-x-auto rounded-md border border-edge bg-surface-1 p-4 text-xs leading-relaxed">
            <code
              className={a.kind === "shell_snapshot" ? "language-bash" : ""}
            >
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
          <pre className="mt-2 overflow-x-auto rounded-md border border-edge bg-surface-1 p-4 text-xs">
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
