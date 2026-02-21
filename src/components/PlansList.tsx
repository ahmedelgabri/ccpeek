import SearchInput from "./SearchInput";
import type { PlanEntry } from "../lib/types";
import { formatBytes } from "../lib/format";

interface Props {
  plans: PlanEntry[];
}

export default function PlansList({ plans }: Props) {
  return (
    <SearchInput
      items={plans}
      searchKeys={(plan) => [plan.title, plan.fileName]}
      placeholder="Filter plans..."
      renderItem={(plan) => (
        <a
          key={plan.fileName}
          href={`/plans/${plan.fileName.replace(".md", "")}/`}
          className="flex items-baseline justify-between gap-4 p-3 rounded bg-zinc-900 border border-zinc-800 hover:border-zinc-700 transition-colors"
        >
          <span className="text-zinc-200 truncate">{plan.title}</span>
          <span className="text-zinc-500 text-xs shrink-0">
            {formatBytes(plan.sizeBytes)}
          </span>
        </a>
      )}
    />
  );
}
