import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { expect, it } from 'vitest'

const web = join(__dirname, '..', '..')

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sourceFiles(path)
    return /\.tsx$/.test(name) && !/\.test\./.test(name) ? [path] : []
  })
}

const files = sourceFiles(join(web, 'src')).map((file) => relative(web, file))

// Brand assets and third-party selectors that must carry literal colours. Everything else uses
// the semantic tokens in src/index.css (chart series are --chart-1..5 there).
const HEX_ALLOW = new Set([
  'src/features/links/LinkQr.tsx', // a QR code is black on white by definition
  'src/components/BrandLogo.tsx', // the brand mark's fixed gradient stops
  'src/components/ui/chart.tsx', // shadcn's selector for recharts' default #ccc grid stroke
])

it('has no raw hex colours in components', () => {
  const offenders = files.flatMap((file) => {
    if (HEX_ALLOW.has(file)) return []
    const hits = readFileSync(join(web, file), 'utf8').match(/#[0-9a-fA-F]{3,8}\b/g)
    return hits ? [`${file}: ${hits.join(', ')}`] : []
  })
  expect(offenders).toEqual([])
})

it('does not dim body text with colour opacity (use muted-foreground)', () => {
  const offenders = files.flatMap((file) => {
    if (file.startsWith('src/components/ui/')) return []
    const hits = readFileSync(join(web, file), 'utf8').match(/\btext-(?:foreground|muted-foreground|card-foreground)\/\d+/g)
    return hits ? [`${file}: ${hits.join(', ')}`] : []
  })
  expect(offenders).toEqual([])
})
