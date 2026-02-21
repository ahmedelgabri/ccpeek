import { useState, useCallback, type ReactNode } from 'react'

interface Props<T> {
	items: T[]
	searchKeys: (item: T) => string[]
	renderItem: (item: T) => ReactNode
	placeholder?: string
}

export default function SearchInput<T>({
	items,
	searchKeys,
	renderItem,
	placeholder = 'Search...',
}: Props<T>) {
	const [query, setQuery] = useState('')

	const filtered = useCallback(() => {
		if (!query.trim()) return items
		const terms = query.toLowerCase().split(/\s+/)
		return items.filter((item) => {
			const keys = searchKeys(item)
				.map((k) => k.toLowerCase())
				.join(' ')
			return terms.every((term) => keys.includes(term))
		})
	}, [items, query, searchKeys])()

	return (
		<div>
			<input
				type="text"
				value={query}
				onChange={(e) => setQuery(e.target.value)}
				placeholder={placeholder}
				className="w-full px-3 py-2 mb-4 bg-zinc-900 border border-zinc-800 rounded text-sm text-zinc-200 placeholder:text-zinc-500 focus:outline-none focus:border-zinc-600"
			/>
			<div className="text-xs text-zinc-500 mb-3">
				{filtered.length} of {items.length} items
			</div>
			<div className="space-y-2">{filtered.map(renderItem)}</div>
		</div>
	)
}
