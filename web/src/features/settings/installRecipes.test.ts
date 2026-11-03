import ts from 'typescript'
import { describe, expect, it } from 'vitest'

import { aiInstallPrompt, buildProxyRecipes, buildRecipes, eventsUrl, firstPartySnippet, parseSnippet, trackerHost, type Recipe } from './installRecipes'

const input = { publicId: 'abc123def456', scriptUrl: 'https://stats.supermetry.com/js/script.js' }
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
      'html', 'npm', 'react', 'nextjs', 'vue', 'nuxt', 'sveltekit', 'astro', 'remix', 'angular', 'wordpress', 'shopify', 'gtm',
      'node', 'express', 'curl', 'python', 'go', 'react-native',
    ])
  })

  it('embeds the real script URL and site id in every recipe', () => {
    for (const recipe of recipes) {
      expect(recipe.code, recipe.id).toContain('abc123def456')
      expect(recipe.code, recipe.id).toMatch(/stats\.supermetry\.com/)
    }
  })

  it('passes site and host to npm packages and uses the script or events URL elsewhere', () => {
    for (const recipe of recipes) {
      if (recipe.install) {
        expect(recipe.install, recipe.id).toMatch(/^npm install @supermetry\//)
        expect(recipe.code, recipe.id).toContain('@supermetry/')
        expect(recipe.code, recipe.id).toContain('https://stats.supermetry.com')
        expect(recipe.code, recipe.id).not.toContain('/js/script.js')
      } else if (recipe.group === 'browser') {
        expect(recipe.code, recipe.id).toContain('/js/script.js')
      } else {
        expect(recipe.code, recipe.id).toContain('/api/public/v1/events')
      }
    }
  })

  it('uses the official package for each supported framework', () => {
    expect(Object.fromEntries(recipes.filter((recipe) => recipe.install).map((recipe) => [recipe.id, recipe.install]))).toEqual({
      npm: 'npm install @supermetry/tracker',
      react: 'npm install @supermetry/react',
      nextjs: 'npm install @supermetry/nextjs',
      vue: 'npm install @supermetry/vue',
      astro: 'npm install @supermetry/astro',
      remix: 'npm install @supermetry/remix',
      node: 'npm install @supermetry/node',
      express: 'npm install @supermetry/express',
      'react-native': 'npm install @supermetry/react-native @react-native-async-storage/async-storage',
    })
  })

  it('produces syntactically valid TypeScript, JSX and JavaScript', () => {
    for (const [id, kind] of [
      ['npm', ts.ScriptKind.TS], ['react', ts.ScriptKind.TSX], ['nextjs', ts.ScriptKind.TSX], ['vue', ts.ScriptKind.TS],
      ['remix', ts.ScriptKind.TSX], ['nuxt', ts.ScriptKind.TS], ['express', ts.ScriptKind.TS], ['react-native', ts.ScriptKind.TSX],
    ] as const) {
      expect(syntaxErrors(byId(id).code, kind), id).toEqual([])
    }
    expect(syntaxErrors(byId('node').code, ts.ScriptKind.JS), 'node').toEqual([])
    // The GTM tag wraps plain JavaScript in a script element; check the inner code.
    const gtm = byId('gtm').code.replace(/^<script>\n?/, '').replace(/<\/script>\s*$/, '')
    expect(syntaxErrors(gtm, ts.ScriptKind.JS), 'gtm').toEqual([])
  })

  it('escapes unusual ids instead of breaking the quoting', () => {
    const odd = buildRecipes({ publicId: 'a"b', scriptUrl: 'https://x.test/js/script.js' })
    for (const id of ['nextjs', 'react', 'remix', 'react-native']) {
      expect(syntaxErrors(odd.find((recipe) => recipe.id === id)!.code, ts.ScriptKind.TSX), id).toEqual([])
    }
  })

  it('derives the host from the tracker URL', () => {
    expect(trackerHost('https://a.example:8443/js/script.full.js')).toBe('https://a.example:8443')
  })

  it('writes an AI assistant prompt with the real site id, host and packages', () => {
    const prompt = aiInstallPrompt(input)
    expect(prompt).toContain('abc123def456')
    expect(prompt).toContain('https://stats.supermetry.com')
    expect(prompt).toContain('https://stats.supermetry.com/js/script.js')
    for (const pkg of ['@supermetry/tracker', '@supermetry/react', '@supermetry/nextjs', '@supermetry/node']) expect(prompt).toContain(pkg)
  })

  it('parses the server snippet and falls back to the page origin', () => {
    expect(parseSnippet('<script defer data-site="zzz" src="https://a.example/js/script.full.js"></script>', 'x', 'https://o.test'))
      .toEqual({ publicId: 'zzz', scriptUrl: 'https://a.example/js/script.full.js' })
    expect(parseSnippet(undefined, 'pid', 'https://o.test')).toEqual({ publicId: 'pid', scriptUrl: 'https://o.test/js/script.js' })
    expect(parseSnippet('<script src="/js/s.js"></script>', 'pid', 'https://o.test').scriptUrl).toBe('https://o.test/js/s.js')
    expect(eventsUrl('https://a.example:8443/js/script.js')).toBe('https://a.example:8443/api/public/v1/events')
  })
})

describe('first-party proxy recipes', () => {
  const cloud = { publicId: 'abc123def456', scriptUrl: 'https://app.supermetry.com/js/abc123def456.js' }
  const proxyById = (recipes: Recipe[], id: string): Recipe => {
    const found = recipes.find((recipe) => recipe.id === id)
    if (!found) throw new Error(`missing proxy recipe ${id}`)
    return found
  }

  it('offers nginx, Caddy, a Cloudflare Worker and Next.js', () => {
    expect(buildProxyRecipes(cloud, { managed: true }).map((recipe) => recipe.id)).toEqual(['proxy-nginx', 'proxy-caddy', 'proxy-cloudflare', 'proxy-nextjs'])
  })

  it('forwards every tracking path to the real host', () => {
    for (const recipe of buildProxyRecipes(cloud, { managed: true })) {
      expect(recipe.code, recipe.id).toContain('app.supermetry.com')
      if (recipe.id === 'proxy-nextjs') continue
      for (const path of ['js/', 'px/', 'api/public/', 'replay', 'api/replay/']) expect(recipe.code, `${recipe.id} ${path}`).toContain(path)
    }
  })

  it('sends the Cloud proxy key and visitor address only on managed instances', () => {
    for (const recipe of buildProxyRecipes(cloud, { managed: true })) {
      expect(recipe.code, recipe.id).toMatch(/X-Supermetry-Proxy-Key|proxyKey/)
    }
    for (const recipe of buildProxyRecipes({ ...cloud, scriptUrl: 'https://stats.example.org/js/abc123def456.js' }, { managed: false })) {
      expect(recipe.code, recipe.id).not.toMatch(/Proxy-Key|proxyKey|smpk_/)
      expect(recipe.note ?? '', recipe.id).toContain('SUPERMETRY_TRUSTED_PROXIES')
    }
  })

  it('never puts a real-looking key in the snippet', () => {
    for (const recipe of buildProxyRecipes(cloud, { managed: true })) {
      expect(recipe.code, recipe.id).not.toMatch(/smpk_[A-Za-z0-9]{8,}/)
    }
  })

  it('gives JavaScript recipes valid syntax', () => {
    const recipes = buildProxyRecipes(cloud, { managed: true })
    expect(syntaxErrors(proxyById(recipes, 'proxy-cloudflare').code, ts.ScriptKind.JS)).toEqual([])
    expect(syntaxErrors(proxyById(recipes, 'proxy-nextjs').code, ts.ScriptKind.TS)).toEqual([])
  })

  it('points the first-party snippet at the proxy prefix', () => {
    expect(firstPartySnippet(cloud)).toBe('<script defer src="/sm/js/abc123def456.js"></script>')
  })
})

describe('first-party proxy recipes with a custom tracker path', () => {
  const custom = { publicId: 'abc123def456', scriptUrl: 'https://stats.example.org/a/abc123def456.js' }

  it('uses the configured script directory in the snippet and every proxy', () => {
    expect(firstPartySnippet(custom)).toBe('<script defer src="/sm/a/abc123def456.js"></script>')
    const recipes = buildProxyRecipes(custom, { managed: false })
    const code = (id: string) => recipes.find((recipe) => recipe.id === id)?.code ?? ''
    expect(code('proxy-nginx')).toContain('^/sm/(a/|px/|api/public/|')
    expect(code('proxy-caddy')).toContain('/sm/a/*')
    expect(code('proxy-cloudflare')).toContain('"/sm/a/"')
    for (const recipe of recipes) expect(recipe.code, recipe.id).not.toContain('/sm/js/')
  })

  it('tells self-hosters to forward a changed event path', () => {
    for (const recipe of buildProxyRecipes(custom, { managed: false })) {
      expect(recipe.note ?? '', recipe.id).toContain('SUPERMETRY_EVENT_PATH')
    }
  })
})
