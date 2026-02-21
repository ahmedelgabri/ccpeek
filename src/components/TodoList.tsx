import type { TodoItem } from '../lib/types'

interface Props {
	items: TodoItem[]
}

const statusStyles: Record<string, string> = {
	completed: 'bg-emerald-900/50 text-emerald-300',
	in_progress: 'bg-amber-900/50 text-amber-300',
	pending: 'bg-zinc-800 text-zinc-400',
}

export default function TodoList({ items }: Props) {
	return (
		<ul className="space-y-2">
			{items.map((item, i) => (
				<li
					key={i}
					className="flex items-start gap-3 p-3 rounded bg-zinc-900 border border-zinc-800"
				>
					<span
						className={`shrink-0 px-2 py-0.5 rounded text-xs font-medium ${statusStyles[item.status] ?? statusStyles.pending}`}
					>
						{item.status}
					</span>
					<span className="text-zinc-200 text-sm">{item.content}</span>
				</li>
			))}
		</ul>
	)
}
