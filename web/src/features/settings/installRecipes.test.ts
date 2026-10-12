import ts from 'typescript'
import { describe, expect, it } from 'vitest'

import { buildRecipes, eventsUrl, parseSnippet, type Recipe } from './installRecipes'

const input = { publicId: 'abc123def456', scriptUrl: 'https://stats.example.com/js/script.js' }
const recipes = buildRecipes(input)
const byId = (id: string): Recipe => {
  const found = recipes.find((recipe) => recipe.id === id)
  if (!found) throw new Error(`missing recipe ${id}`)
  return found
}

function syntaxErrors(code: string, kind: ts.ScriptKind): string[] {
  const result = ts.transpileModule(code, {
    reportDiagnostics: true,
    fileName: kind === ts.ScriptKind.TSX ? 'x.tsx' : kind === ts.ScriptKind.TS ? 'x.ts' : 'x.js',
    compilerOptions: { jsx: ts.JsxEmit.Preserve, target: ts.ScriptTarget.ES2022, allowJs: true },
  })
  return (result.diagnostics ?? []).map((diagnostic) => ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n'))
}

describe('install recipes', () => {
  it('covers every documented platform once', () => {
    expect(recipes.map((recipe) => recipe.id)).toEqual([
      'html', 'react', 'nextjs', 'vue', 'nuxt', 'sveltekit', 'astro', 'remix', 'angular', 'wordpress', 'shopify', 'gtm', 'curl', 'node', 'python', 'go',
    ])
  })

  it('embeds the real script URL and site id in every recipe', () => {
    for (const recipe of recipes) {
      expect(recipe.code, recipe.id).toContain('abc123def456')
      expect(recipe.code, recipe.id).toMatch(/stats\.example\.com/)
    }
  })

  it('keeps browser recipes on the script URL and server recipes on the events endpoint', () => {
    for (const recipe of recipes.filter((item) => item.group === 'browser')) expect(recipe.code, recipe.id).toContain('/js/script.js')
    for (const recipe of recipes.filter((item) => item.group === 'server')) expect(recipe.code, recipe.id).toContain('/api/public/v1/events')
  })

  it('produces syntactically valid TypeScript, JSX and JavaScript', () => {
    for (const [id, kind] of [['nextjs', ts.ScriptKind.TSX], ['remix', ts.ScriptKind.TSX], ['nuxt', ts.ScriptKind.TS]] as const) {
      expect(syntaxErrors(byId(id).code, kind), id).toEqual([])
    }
    expect(syntaxErrors(byId('node').code, ts.ScriptKind.JS), 'node').toEqual([])
    // The GTM tag wraps plain JavaScript in a script element; check the inner code.
    const gtm = byId('gtm').code.replace(/^<script>\n?/, '').replace(/<\/script>\s*$/, '')
    expect(syntaxErrors(gtm, ts.ScriptKind.JS), 'gtm').toEqual([])
  })

  it('escapes unusual ids instead of breaking the quoting', () => {
    const odd = buildRecipes({ publicId: 'a"b', scriptUrl: 'https://x.test/js/script.js' })
    expect(syntaxErrors(odd.find((recipe) => recipe.id === 'nextjs')!.code, ts.ScriptKind.TSX)).toEqual([])
  })

  it('parses the server snippet and falls back to the page origin', () => {
    expect(parseSnippet('<script defer data-site="zzz" src="https://a.example/js/script.full.js"></script>', 'x', 'https://o.test'))
      .toEqual({ publicId: 'zzz', scriptUrl: 'https://a.example/js/script.full.js' })
    expect(parseSnippet(undefined, 'pid', 'https://o.test')).toEqual({ publicId: 'pid', scriptUrl: 'https://o.test/js/script.js' })
    expect(parseSnippet('<script src="/js/s.js"></script>', 'pid', 'https://o.test').scriptUrl).toBe('https://o.test/js/s.js')
    expect(eventsUrl('https://a.example:8443/js/script.js')).toBe('https://a.example:8443/api/public/v1/events')
  })
})
