import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sources(path)
    return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [path] : []
  })
}

// Modules that may use cmdk because they are only ever loaded with a dynamic import().
const lazyOnly = new Set(['features/sites/TimezoneList.tsx'])

describe('palette code splitting', () => {
  it('imports CommandPalette, ui/command and cmdk statically only from the palette itself', () => {
    const root = join(__dirname, '..')
    const offenders = sources(root).filter((file) => {
      const name = relative(root, file)
      if (name === 'components/CommandPalette.tsx' || name === 'components/ui/command.tsx' || lazyOnly.has(name)) return false
      const text = readFileSync(file, 'utf8')
      return /^\s*(?:import|export)\b[^\n]*from\s+['"](?:\.\/CommandPalette|@\/components\/CommandPalette|@\/components\/ui\/command|\.\/ui\/command|cmdk)['"]/m.test(text)
    })
    expect(offenders.map((file) => relative(root, file))).toEqual([])
  })

  it('loads the cmdk-based lazy modules only through a dynamic import', () => {
    const root = join(__dirname, '..')
    const offenders = sources(root).filter((file) => {
      const text = readFileSync(file, 'utf8')
      return [...lazyOnly].some((name) => {
        const base = name.replace(/\.tsx$/, '').split('/').pop()
        return new RegExp(`^\\s*(?:import|export)\\b[^\\n]*from\\s+['"][^'"]*/${base}['"]`, 'm').test(text)
      })
    })
    expect(offenders.map((file) => relative(root, file))).toEqual([])
  })
})
