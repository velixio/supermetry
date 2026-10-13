import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..', '..') // web/

// Files that render outside AppShell (sign-in and the public share page):
// they own their layout and keep a hand-rolled h1.
const OUTSIDE_SHELL = new Set<string>([
  'src/features/auth/AuthScreen.tsx',
  'src/features/share/SharedDashboard.tsx',
])

// Files still to migrate. Each adoption task deletes its entries.
const PENDING = new Set<string>([])

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sourceFiles(path)
    return /\.tsx$/.test(name) && !/\.test\./.test(name) ? [path] : []
  })
}

describe('page frame adoption', () => {
  const withH1 = [...sourceFiles(join(root, 'src/routes')), ...sourceFiles(join(root, 'src/features')), ...sourceFiles(join(root, 'src/components'))]
    .filter((file) => relative(root, file) !== 'src/components/PageHeader.tsx')
    .filter((file) => readFileSync(file, 'utf8').includes('<h1'))
    .map((file) => relative(root, file))

  it('has no hand-rolled h1 outside the allowlists', () => {
    const unexpected = withH1.filter((file) => !OUTSIDE_SHELL.has(file) && !PENDING.has(file))
    expect(unexpected).toEqual([])
  })

  it('keeps PENDING free of files that already migrated', () => {
    const stale = [...PENDING].filter((file) => !withH1.includes(file))
    expect(stale).toEqual([])
  })

  it('keeps OUTSIDE_SHELL free of files without an h1', () => {
    const stale = [...OUTSIDE_SHELL].filter((file) => !withH1.includes(file))
    expect(stale).toEqual([])
  })
})
