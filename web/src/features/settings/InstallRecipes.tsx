import { useEffect, useMemo, useRef, useState } from 'react'
import { CheckIcon, CopyIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { copyText } from '@/lib/clipboard'
import { buildRecipes, parseSnippet, type Recipe } from './installRecipes'

export type VerifyResult = { status?: 'waiting' | 'receiving' | 'quiet'; lastSeenAt?: string | null }

const pollEveryMs = 3_000
const maxPolls = 20

type VerifyState = 'idle' | 'checking' | 'ok' | 'timeout' | 'error'

/** Polls the install status until an event newer than the click arrives. */
export function VerifyInstallation({ verify, intervalMs = pollEveryMs }: { verify: () => Promise<VerifyResult | undefined>; intervalMs?: number }) {
  const [state, setState] = useState<VerifyState>('idle')
  const run = useRef(0)
  useEffect(() => () => { run.current += 1 }, [])

  async function start() {
    const mine = ++run.current
    const startedAt = Date.now()
    setState('checking')
    for (let attempt = 0; attempt < maxPolls; attempt++) {
      try {
        const result = await verify()
        if (mine !== run.current) return
        const seen = result?.lastSeenAt ? new Date(result.lastSeenAt).getTime() : 0
        if (result?.status === 'receiving' || seen >= startedAt) {
          setState('ok')
          return
        }
      } catch {
        if (mine === run.current) setState('error')
        return
      }
      await new Promise((resolve) => setTimeout(resolve, intervalMs))
      if (mine !== run.current) return
    }
    setState('timeout')
  }

  return (
    <div className="flex flex-wrap items-center gap-3">
      <Button type="button" variant="outline" className="min-h-11" disabled={state === 'checking'} onClick={() => void start()}>
        Verify installation
      </Button>
      <span role="status" className="text-sm text-muted-foreground">
        {state === 'checking' ? 'Waiting for the first event… open your site in another tab.' : null}
        {state === 'ok' ? 'Events are arriving. Installation verified.' : null}
        {state === 'timeout' ? 'No event yet. Check that the snippet is on the page, then try again.' : null}
        {state === 'error' ? 'Could not check the status. Try again.' : null}
      </span>
    </div>
  )
}

function RecipePanel({ recipe }: { recipe: Recipe }) {
  const [copied, setCopied] = useState<'yes' | 'manual' | null>(null)
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <p className="text-sm"><span className="font-medium">Where:</span> {recipe.where}</p>
      {recipe.note ? <p className="text-sm text-muted-foreground">{recipe.note}</p> : null}
      <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs select-all" tabIndex={0} aria-label={`${recipe.label} snippet`}><code>{recipe.code}</code></pre>
      <div className="flex items-center gap-3">
        <Button type="button" variant="outline" className="min-h-11" onClick={() => void copyText(recipe.code).then((ok) => setCopied(ok ? 'yes' : 'manual'))}>
          {copied === 'yes' ? <CheckIcon data-icon="inline-start" /> : <CopyIcon data-icon="inline-start" />}
          Copy {recipe.label} snippet
        </Button>
        {copied ? <span role="status" className="text-sm text-muted-foreground">{copied === 'yes' ? 'Copied' : 'Select the snippet and press Ctrl+C to copy'}</span> : null}
      </div>
    </div>
  )
}

export function InstallRecipes({ publicId, snippet, verify }: { publicId: string; snippet?: string; verify?: () => Promise<VerifyResult | undefined> }) {
  const recipes = useMemo(() => buildRecipes(parseSnippet(snippet, publicId, window.location.origin)), [publicId, snippet])
  const groups: { label: string; items: Recipe[] }[] = [
    { label: 'In the browser', items: recipes.filter((recipe) => recipe.group === 'browser') },
    { label: 'From your server', items: recipes.filter((recipe) => recipe.group === 'server') },
  ]
  return (
    <section aria-labelledby="install-recipes-title" className="flex min-w-0 flex-col gap-3">
      <h2 id="install-recipes-title" className="text-lg font-semibold">Install recipes</h2>
      <p className="text-sm text-muted-foreground">Pick your stack. Every snippet already contains this site&apos;s real tracker URL and id.</p>
      <Tabs defaultValue="html" className="min-w-0">
        <TabsList aria-label="Installation method" className="h-auto max-w-full flex-wrap justify-start">
          {groups.flatMap((group) => group.items).map((recipe) => (
            <TabsTrigger key={recipe.id} value={recipe.id} className="h-11 px-3">{recipe.label}</TabsTrigger>
          ))}
        </TabsList>
        {recipes.map((recipe) => (
          <TabsContent key={recipe.id} value={recipe.id} className="min-w-0 pt-3"><RecipePanel recipe={recipe} /></TabsContent>
        ))}
      </Tabs>
      {verify ? <VerifyInstallation verify={verify} /> : null}
    </section>
  )
}
