import { copyFile, mkdir, readdir, rm } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const packageRoot = path.join(webRoot, 'node_modules', 'country-flag-icons')
const sourceRoot = path.join(packageRoot, '3x2')
const destinationRoot = path.join(webRoot, 'public', 'flags')

await mkdir(destinationRoot, { recursive: true })

for (const name of await readdir(destinationRoot)) {
  if (name.endsWith('.svg') || name === 'LICENSE') {
    await rm(path.join(destinationRoot, name))
  }
}

const sourceNames = (await readdir(sourceRoot)).filter((name) => name.endsWith('.svg'))
await Promise.all(
  sourceNames.map((name) =>
    copyFile(path.join(sourceRoot, name), path.join(destinationRoot, name.toLowerCase())),
  ),
)
await copyFile(path.join(packageRoot, 'LICENSE'), path.join(destinationRoot, 'LICENSE'))

console.log(`Copied ${sourceNames.length} country flags and their license.`)
