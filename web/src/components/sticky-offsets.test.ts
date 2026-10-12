import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { expect, it } from 'vitest'

const web = join(__dirname, '..', '..') // web/

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sourceFiles(path)
    return /\.tsx$/.test(name) && !/\.test\./.test(name) ? [path] : []
  })
}

// The app header is the only sticky bar above content: 44px (h-11) at top-0.
// Anything that sticks inside a page must clear it; dialogs and AccountFrame have their own scroll context.
const EXPECTED = [
  'src/components/AccountFrame.tsx: sticky top-0',
  'src/components/AppHeader.tsx: sticky top-0',
  'src/components/DashboardPanels.tsx: sticky top-28',
  'src/components/FullReportDialog.tsx: sticky top-0',
  'src/features/settings/SettingsPages.tsx: lg:sticky lg:top-17',
  'src/routes/app.$org.$site.index.tsx: sticky top-11',
]

it('lists every sticky top offset and keeps them clear of the 44px app header', () => {
  const files = sourceFiles(join(web, 'src'))
  const found = files.flatMap((file) =>
    [...readFileSync(file, 'utf8').matchAll(/((?:[\w-]+:)?sticky)\b[^"'`\n]*?\s((?:[\w-]+:)?top-[^\s"'`]+)/g)]
      .map((match) => `${relative(web, file)}: ${match[1]} ${match[2]}`),
  )
  expect(found.sort()).toEqual([...EXPECTED].sort())
  for (const file of files) expect(readFileSync(file, 'utf8')).not.toMatch(/\btop-9\b/)
})
