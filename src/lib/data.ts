import fs from 'node:fs'
import path from 'node:path'
import type { IndexData } from './types'

// process.cwd() is the project root during both dev and build
const DATA_DIR = path.join(process.cwd(), 'src', 'data')

export function getIndexData(): IndexData {
	const raw = fs.readFileSync(path.join(DATA_DIR, 'index.json'), 'utf-8')
	return JSON.parse(raw)
}

export function readDataFile(relativePath: string): string {
	return fs.readFileSync(path.join(DATA_DIR, relativePath), 'utf-8')
}

export function dataFileExists(relativePath: string): boolean {
	return fs.existsSync(path.join(DATA_DIR, relativePath))
}
