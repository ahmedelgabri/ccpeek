import { describe, it, expect } from 'vitest'
import {
	decodeProjectDir,
	formatBytes,
	formatTimestamp,
	formatDate,
	truncate,
} from '../../../src/lib/format'

describe('decodeProjectDir', () => {
	it('decodes a standard path', () => {
		expect(decodeProjectDir('-Users-ahmed-code-personal-dev')).toBe(
			'/Users/ahmed/code/personal/dev',
		)
	})

	it('decodes a path with dotfile (double dash)', () => {
		expect(decodeProjectDir('-Users-ahmed--dotfiles')).toBe('/Users/ahmed/.dotfiles')
	})

	it('handles a plain name without leading dash', () => {
		expect(decodeProjectDir('some-project')).toBe('some/project')
	})
})

describe('formatBytes', () => {
	it('formats zero bytes', () => {
		expect(formatBytes(0)).toBe('0 B')
	})

	it('formats bytes', () => {
		expect(formatBytes(500)).toBe('500 B')
	})

	it('formats kilobytes', () => {
		expect(formatBytes(1024)).toBe('1.0 KB')
	})

	it('formats megabytes', () => {
		expect(formatBytes(1048576)).toBe('1.0 MB')
	})

	it('formats fractional kilobytes', () => {
		expect(formatBytes(1536)).toBe('1.5 KB')
	})
})

describe('formatTimestamp', () => {
	it('formats a millisecond timestamp', () => {
		const result = formatTimestamp(1757025535437)
		expect(result).toMatch(/Sep/)
		expect(result).toMatch(/2025/)
	})
})

describe('formatDate', () => {
	it('formats an ISO date string', () => {
		const result = formatDate('2025-12-06T10:21:59.188Z')
		expect(result).toMatch(/Dec/)
		expect(result).toMatch(/2025/)
	})
})

describe('truncate', () => {
	it('returns short strings unchanged', () => {
		expect(truncate('hello', 10)).toBe('hello')
	})

	it('truncates long strings with ellipsis', () => {
		expect(truncate('hello world', 8)).toBe('hello...')
	})

	it('handles exact length', () => {
		expect(truncate('hello', 5)).toBe('hello')
	})
})
