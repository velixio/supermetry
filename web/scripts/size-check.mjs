import { gzipSync } from 'node:zlib'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const budget = 256_000
const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const distRoot = path.join(webRoot, 'dist')
const manifest = JSON.parse(
  await readFile(path.join(distRoot, '.vite', 'manifest.json'), 'utf8'),
)
const entries = Object.values(manifest).filter((chunk) => chunk.isEntry)
const files = new Set()

function collect(chunk) {
  if (files.has(chunk.file)) return
  files.add(chunk.file)
  for (const imported of chunk.imports ?? []) {
    const importedChunk = manifest[imported]
    if (!importedChunk) {
      throw new Error(`Missing static import ${imported} from build manifest`)
    }
    collect(importedChunk)
  }
}

for (const entry of entries) collect(entry)

let total = 0
for (const file of files) {
  if (!file.endsWith('.js')) continue
  total += gzipSync(await readFile(path.join(distRoot, file))).byteLength
}

if (total > budget) {
  throw new Error(`Initial JavaScript is ${total} bytes gzipped; budget is ${budget}`)
}

console.log(`Initial JavaScript: ${total} / ${budget} bytes gzipped`)
