// The Insights tables share one look: label columns on the left, numeric
// columns right-aligned in tabular mono.
export function TableHead({
  labels,
  numbers,
}: {
  labels: string[];
  numbers: string[];
}) {
  return (
    <thead className="text-left font-mono text-micro text-ink-faint">
      <tr>
        {labels.map((c) => (
          <th key={c} className="px-3 py-1.5 font-normal">
            {c}
          </th>
        ))}
        {numbers.map((c) => (
          <th key={c} className="px-3 py-1.5 text-right font-normal">
            {c}
          </th>
        ))}
      </tr>
    </thead>
  );
}

export const TABLE_BODY = "divide-y divide-edge font-mono text-xs tabular-nums";
