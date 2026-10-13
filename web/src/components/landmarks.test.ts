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

// Comments may mention <main>; only real markup counts.
function stripComments(code: string): string {
  return code.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|\s)\/\/.*$/gm, '$1')
}

// Every /app page renders inside AppShell, which owns the only <main> (id="main-content").
// Only screens that never render inside the shell may own one.
const OWN_MAIN = [
  'src/components/AppShell.tsx',
  'src/features/auth/AuthScreen.tsx', // sign-in, sign-up, reset: outside /app
  'src/features/share/SharedDashboard.tsx', // public share page: outside /app
  'src/lib/session-lifecycle.tsx', // log-out screen replaces the whole app
  'src/routes/app.tsx', // loading state before the shell exists
]

it('keeps <main> to the shell and the screens outside it', () => {
  const found = sourceFiles(join(web, 'src'))
    .filter((file) => /<main[\s>]|role=["']main["']/.test(stripComments(readFileSync(file, 'utf8'))))
    .map((file) => relative(web, file))
  expect(found.sort()).toEqual([...OWN_MAIN].sort())
})

it('sizes nothing against the removed 56px account header', () => {
  for (const file of sourceFiles(join(web, 'src'))) {
    expect(readFileSync(file, 'utf8'), relative(web, file)).not.toMatch(/100dvh-3\.5rem|\bh-14\b/)
  }
})
