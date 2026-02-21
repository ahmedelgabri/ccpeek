import SearchInput from "./SearchInput";
import type { ProjectEntry } from "../lib/types";

interface Props {
  projects: ProjectEntry[];
}

export default function ProjectsList({ projects }: Props) {
  return (
    <SearchInput
      items={projects}
      searchKeys={(p) => [p.displayName, p.dirName]}
      placeholder="Filter projects..."
      renderItem={(project) => (
        <a
          key={project.dirName}
          href={`/projects/${project.dirName}/`}
          className="flex items-baseline justify-between gap-4 p-3 rounded bg-zinc-900 border border-zinc-800 hover:border-zinc-700 transition-colors"
        >
          <span className="text-zinc-200 text-sm truncate">
            {project.displayName}
          </span>
          <span className="text-zinc-500 text-xs shrink-0">
            {project.sessionCount} sessions
          </span>
        </a>
      )}
    />
  );
}
