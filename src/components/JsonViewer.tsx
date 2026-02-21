import { useState } from "react";

interface Props {
  data: unknown;
  defaultExpanded?: boolean;
}

function JsonNode({
  value,
  depth,
  defaultExpanded,
}: {
  value: unknown;
  depth: number;
  defaultExpanded: boolean;
}) {
  const [expanded, setExpanded] = useState(defaultExpanded && depth < 2);

  if (value === null) return <span className="text-zinc-500">null</span>;
  if (typeof value === "boolean")
    return <span className="text-amber-400">{String(value)}</span>;
  if (typeof value === "number")
    return <span className="text-emerald-400">{value}</span>;
  if (typeof value === "string") {
    if (value.length > 200 && !expanded) {
      return (
        <span>
          <span className="text-sky-400">"{value.slice(0, 200)}</span>
          <button
            onClick={() => setExpanded(true)}
            className="text-zinc-500 hover:text-zinc-300 ml-1"
          >
            ...({value.length} chars)
          </button>
          <span className="text-sky-400">"</span>
        </span>
      );
    }
    return <span className="text-sky-400">"{value}"</span>;
  }

  if (Array.isArray(value)) {
    if (value.length === 0) return <span className="text-zinc-500">[]</span>;
    return (
      <span>
        <button
          onClick={() => setExpanded(!expanded)}
          className="text-zinc-500 hover:text-zinc-300"
        >
          {expanded ? "▼" : "▶"} [{value.length}]
        </button>
        {expanded && (
          <div className="ml-4 border-l border-zinc-800 pl-2">
            {value.map((item, i) => (
              <div key={i}>
                <JsonNode
                  value={item}
                  depth={depth + 1}
                  defaultExpanded={defaultExpanded}
                />
                {i < value.length - 1 && (
                  <span className="text-zinc-600">,</span>
                )}
              </div>
            ))}
          </div>
        )}
      </span>
    );
  }

  if (typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>);
    if (entries.length === 0)
      return <span className="text-zinc-500">{"{}"}</span>;
    return (
      <span>
        <button
          onClick={() => setExpanded(!expanded)}
          className="text-zinc-500 hover:text-zinc-300"
        >
          {expanded ? "▼" : "▶"} {"{"}
          {entries.length}
          {"}"}
        </button>
        {expanded && (
          <div className="ml-4 border-l border-zinc-800 pl-2">
            {entries.map(([key, val], i) => (
              <div key={key}>
                <span className="text-purple-400">"{key}"</span>
                <span className="text-zinc-500">: </span>
                <JsonNode
                  value={val}
                  depth={depth + 1}
                  defaultExpanded={defaultExpanded}
                />
                {i < entries.length - 1 && (
                  <span className="text-zinc-600">,</span>
                )}
              </div>
            ))}
          </div>
        )}
      </span>
    );
  }

  return <span className="text-zinc-500">{String(value)}</span>;
}

export default function JsonViewer({ data, defaultExpanded = false }: Props) {
  return (
    <pre className="text-xs font-mono overflow-x-auto p-3 bg-zinc-950 rounded border border-zinc-800">
      <JsonNode value={data} depth={0} defaultExpanded={defaultExpanded} />
    </pre>
  );
}
