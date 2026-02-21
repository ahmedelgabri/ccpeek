/**
 * Decode a project directory name back to a readable path.
 * e.g. "-Users-ahmed--dotfiles" -> "/Users/ahmed/.dotfiles"
 *
 * The encoding replaces "/" with "-" and "." with "-" at the start of
 * path segments. A double dash "--" indicates a segment starting with ".".
 */
export function decodeProjectDir(dirName: string): string {
	// Replace leading dash with /
	let path = dirName.startsWith('-') ? '/' + dirName.slice(1) : dirName
	// Replace remaining dashes: "--" is "/." and "-" is "/"
	path = path.replace(/--/g, '/.').replace(/-/g, '/')
	return path
}

/**
 * Format a file size in bytes to a human-readable string.
 */
export function formatBytes(bytes: number): string {
	if (bytes === 0) return '0 B'
	const units = ['B', 'KB', 'MB', 'GB']
	const i = Math.floor(Math.log(bytes) / Math.log(1024))
	const value = bytes / Math.pow(1024, i)
	return `${value.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

/**
 * Format a timestamp (ms since epoch) to a locale date string.
 */
export function formatTimestamp(ms: number): string {
	return new Date(ms).toLocaleDateString('en-US', {
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit',
	})
}

/**
 * Format an ISO date string to a short locale string.
 */
export function formatDate(iso: string): string {
	return new Date(iso).toLocaleDateString('en-US', {
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit',
	})
}

/**
 * Truncate a string to a max length, appending "..." if truncated.
 */
export function truncate(str: string, maxLength: number): string {
	if (str.length <= maxLength) return str
	return str.slice(0, maxLength - 3) + '...'
}
