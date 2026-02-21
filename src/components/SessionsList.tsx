import SearchInput from './SearchInput'
import type { SessionEntry } from '../lib/types'
import { formatDate, truncate } from '../lib/format'

interface Props {
	sessions: SessionEntry[]
	dirName: string
}

export default function SessionsList({ sessions, dirName }: Props) {
	return (
		<SearchInput
			items={sessions}
			searchKeys={(s) => [s.firstPrompt, s.sessionId, s.gitBranch ?? '']}
			placeholder="Filter sessions..."
			renderItem={(session) => (
				<a
					key={session.sessionId}
					href={`/projects/${dirName}/${session.sessionId}/`}
					className="block p-3 rounded bg-zinc-900 border border-zinc-800 hover:border-zinc-700 transition-colors"
				>
					<div className="flex items-baseline justify-between gap-4 mb-1">
						<span className="text-zinc-200 text-sm truncate">
							{session.firstPrompt ? truncate(session.firstPrompt, 120) : session.sessionId}
						</span>
						<span className="text-zinc-500 text-xs shrink-0">{session.messageCount} msgs</span>
					</div>
					<div className="flex gap-4 text-xs text-zinc-500">
						{session.created && <span>{formatDate(session.created)}</span>}
						{session.gitBranch && <span className="font-mono">{session.gitBranch}</span>}
					</div>
				</a>
			)}
		/>
	)
}
