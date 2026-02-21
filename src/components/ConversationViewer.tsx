import { useState, useMemo } from 'react'
import type { ConversationMessage, ContentBlock, ToolUseBlock, ToolResultBlock } from '../lib/types'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import JsonViewer from './JsonViewer'

interface Props {
	messages: ConversationMessage[]
}

const PAGE_SIZE = 50

function TextContent({ text }: { text: string }) {
	return (
		<div className="prose prose-invert prose-zinc prose-sm max-w-none">
			<Markdown remarkPlugins={[remarkGfm]}>{text}</Markdown>
		</div>
	)
}

function ToolUseContent({ block }: { block: ToolUseBlock }) {
	const [expanded, setExpanded] = useState(false)

	return (
		<div className="my-2 border border-zinc-800 rounded overflow-hidden">
			<button
				onClick={() => setExpanded(!expanded)}
				className="w-full flex items-center gap-2 px-3 py-2 bg-zinc-900 hover:bg-zinc-800/80 text-left text-sm"
			>
				<span className="text-zinc-500">{expanded ? '▼' : '▶'}</span>
				<span className="text-amber-400 font-mono text-xs">{block.name}</span>
			</button>
			{expanded && (
				<div className="p-2">
					<JsonViewer data={block.input} defaultExpanded />
				</div>
			)}
		</div>
	)
}

function ToolResultContent({ block }: { block: ToolResultBlock }) {
	const [expanded, setExpanded] = useState(false)
	const content =
		typeof block.content === 'string'
			? block.content
			: (block.content
					?.map((c) => c.text || '')
					.filter(Boolean)
					.join('\n') ?? '')

	if (!content) return null

	return (
		<div className="my-2 border border-zinc-800 rounded overflow-hidden">
			<button
				onClick={() => setExpanded(!expanded)}
				className="w-full flex items-center gap-2 px-3 py-2 bg-zinc-900/50 hover:bg-zinc-800/50 text-left text-sm"
			>
				<span className="text-zinc-500">{expanded ? '▼' : '▶'}</span>
				<span className="text-zinc-400 text-xs">tool result</span>
				<span className="text-zinc-600 text-xs truncate">{content.slice(0, 80)}</span>
			</button>
			{expanded && (
				<pre className="p-3 text-xs text-zinc-400 overflow-x-auto max-h-96 overflow-y-auto">
					{content}
				</pre>
			)}
		</div>
	)
}

function MessageContent({ content }: { content: string | ContentBlock[] | undefined }) {
	if (!content) return null

	if (typeof content === 'string') {
		return <TextContent text={content} />
	}

	if (!Array.isArray(content)) return null

	return (
		<>
			{content.map((block, i) => {
				if (!block?.type) return null
				switch (block.type) {
					case 'text':
						return <TextContent key={i} text={block.text} />
					case 'tool_use':
						return <ToolUseContent key={i} block={block} />
					case 'tool_result':
						return <ToolResultContent key={i} block={block} />
					default:
						return null
				}
			})}
		</>
	)
}

const roleStyles: Record<string, string> = {
	user: 'border-l-sky-500 bg-sky-950/20',
	assistant: 'border-l-emerald-500 bg-emerald-950/20',
	system: 'border-l-zinc-500 bg-zinc-900/50',
}

const roleLabels: Record<string, string> = {
	user: 'User',
	assistant: 'Assistant',
	system: 'System',
}

export default function ConversationViewer({ messages }: Props) {
	const [page, setPage] = useState(0)
	const totalPages = Math.ceil(messages.length / PAGE_SIZE)
	const pageMessages = useMemo(
		() => messages.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE),
		[messages, page],
	)

	return (
		<div>
			{totalPages > 1 && (
				<div className="flex items-center gap-2 mb-4">
					<button
						onClick={() => setPage(Math.max(0, page - 1))}
						disabled={page === 0}
						className="px-3 py-1 text-sm rounded bg-zinc-800 text-zinc-300 disabled:opacity-50 hover:bg-zinc-700"
					>
						Prev
					</button>
					<span className="text-sm text-zinc-400">
						Page {page + 1} of {totalPages} ({messages.length} messages)
					</span>
					<button
						onClick={() => setPage(Math.min(totalPages - 1, page + 1))}
						disabled={page >= totalPages - 1}
						className="px-3 py-1 text-sm rounded bg-zinc-800 text-zinc-300 disabled:opacity-50 hover:bg-zinc-700"
					>
						Next
					</button>
				</div>
			)}

			<div className="space-y-3">
				{pageMessages.map((msg, i) => (
					<div
						key={msg.uuid || i}
						className={`border-l-2 pl-4 py-3 rounded-r ${roleStyles[msg.type] ?? roleStyles.system}`}
					>
						<div className="flex items-baseline gap-2 mb-2">
							<span className="text-xs font-medium text-zinc-400">
								{roleLabels[msg.type] ?? msg.type}
							</span>
							{msg.timestamp && (
								<span className="text-xs text-zinc-600">
									{new Date(msg.timestamp).toLocaleString()}
								</span>
							)}
						</div>
						<MessageContent content={msg.message?.content} />
					</div>
				))}
			</div>

			{totalPages > 1 && (
				<div className="flex items-center gap-2 mt-4">
					<button
						onClick={() => setPage(Math.max(0, page - 1))}
						disabled={page === 0}
						className="px-3 py-1 text-sm rounded bg-zinc-800 text-zinc-300 disabled:opacity-50 hover:bg-zinc-700"
					>
						Prev
					</button>
					<span className="text-sm text-zinc-400">
						Page {page + 1} of {totalPages}
					</span>
					<button
						onClick={() => setPage(Math.min(totalPages - 1, page + 1))}
						disabled={page >= totalPages - 1}
						className="px-3 py-1 text-sm rounded bg-zinc-800 text-zinc-300 disabled:opacity-50 hover:bg-zinc-700"
					>
						Next
					</button>
				</div>
			)}
		</div>
	)
}
