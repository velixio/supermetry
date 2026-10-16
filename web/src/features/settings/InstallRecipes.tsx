import { useEffect, useMemo, useRef, useState } from 'react'
import { CheckIcon, CopyIcon, SparklesIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { copyText } from '@/lib/clipboard'
import { aiInstallPrompt, buildRecipes, parseSnippet, trackerHost, type Recipe } from './installRecipes'

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

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState<'yes' | 'manual' | null>(null)
  return (
    <div className="flex items-center gap-3">
      <Button type="button" variant="outline" className="min-h-11" onClick={() => void copyText(value).then((ok) => setCopied(ok ? 'yes' : 'manual'))}>
        {copied === 'yes' ? <CheckIcon data-icon="inline-start" /> : <CopyIcon data-icon="inline-start" />}
        {label}
      </Button>
      {copied ? <span role="status" className="text-sm text-muted-foreground">{copied === 'yes' ? 'Copied' : 'Select the text and press Ctrl+C to copy'}</span> : null}
    </div>
  )
}

function useRecipeInput(publicId: string, snippet?: string) {
  return useMemo(() => parseSnippet(snippet, publicId, window.location.origin), [publicId, snippet])
}

/** The two values every SDK needs, shown on their own so they are easy to find and copy. */
export function SiteDetails({ publicId, snippet }: { publicId: string; snippet?: string }) {
  const input = useRecipeInput(publicId, snippet)
  const host = trackerHost(input.scriptUrl)
  const fields = [
    { label: 'Site ID', value: input.publicId },
    { label: 'Host', value: host },
  ]
  return (
    <section aria-labelledby="site-details-title" className="flex min-w-0 flex-col gap-3">
      <h2 id="site-details-title" className="text-lg font-semibold">Your site details</h2>
      <p className="text-sm text-muted-foreground">
        The npm packages and server SDKs need these two values: pass them as <code>site</code> and <code>host</code>.
      </p>
      <dl className="grid min-w-0 gap-3 sm:grid-cols-2">
        {fields.map((field) => (
          <div key={field.label} className="flex min-w-0 flex-col gap-2 rounded-lg border p-3">
            <dt className="text-sm font-medium">{field.label}</dt>
            <dd className="min-w-0 break-all rounded-md bg-muted px-3 py-2 font-mono text-sm select-all" aria-label={field.label}>{field.value}</dd>
            <CopyButton value={field.value} label={`Copy ${field.label === 'Site ID' ? 'site ID' : 'host'}`} />
          </div>
        ))}
      </dl>
    </section>
  )
}

/** A ready-to-paste prompt for AI coding assistants, with this site's values filled in. */
export function AiInstallPrompt({ publicId, snippet }: { publicId: string; snippet?: string }) {
  const input = useRecipeInput(publicId, snippet)
  const prompt = useMemo(() => aiInstallPrompt(input), [input])
  return (
    <section aria-labelledby="ai-install-title" className="flex min-w-0 flex-col gap-3">
      <h2 id="ai-install-title" className="flex items-center gap-2 text-lg font-semibold"><SparklesIcon aria-hidden="true" className="size-4" />Install with an AI assistant</h2>
      <p className="text-sm text-muted-foreground">Copy this prompt into your AI coding assistant. It already contains this site&apos;s ID and host.</p>
      <pre className="max-h-64 overflow-auto rounded-lg bg-muted p-3 text-xs whitespace-pre-wrap select-all" tabIndex={0} aria-label="AI install prompt"><code>{prompt}</code></pre>
      <CopyButton value={prompt} label="Copy AI prompt" />
    </section>
  )
}

function RecipePanel({ recipe }: { recipe: Recipe }) {
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <p className="text-sm"><span className="font-medium">Where:</span> {recipe.where}</p>
      {recipe.note ? <p className="text-sm text-muted-foreground">{recipe.note}</p> : null}
      {recipe.install ? (
        <div className="flex min-w-0 flex-col gap-2">
          <p className="text-sm font-medium">1. Install the package</p>
          <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs select-all" tabIndex={0} aria-label={`${recipe.label} install command`}><code>{recipe.install}</code></pre>
          <CopyButton value={recipe.install} label={`Copy ${recipe.label} install command`} />
          <p className="text-sm font-medium">2. Add it to {recipe.where}</p>
        </div>
      ) : null}
      <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs select-all" tabIndex={0} aria-label={`${recipe.label} snippet`}><code>{recipe.code}</code></pre>
      <CopyButton value={recipe.code} label={`Copy ${recipe.label} snippet`} />
    </div>
  )
}

export function InstallRecipes({ publicId, snippet, verify }: { publicId: string; snippet?: string; verify?: () => Promise<VerifyResult | undefined> }) {
  const input = useRecipeInput(publicId, snippet)
  const recipes = useMemo(() => buildRecipes(input), [input])
  const groups: { label: string; items: Recipe[] }[] = [
    { label: 'In the browser', items: recipes.filter((recipe) => recipe.group === 'browser') },
    { label: 'From your server', items: recipes.filter((recipe) => recipe.group === 'server') },
    { label: 'In a mobile app', items: recipes.filter((recipe) => recipe.group === 'mobile') },
  ]
  return (
    <section aria-labelledby="install-recipes-title" className="flex min-w-0 flex-col gap-3">
      <h2 id="install-recipes-title" className="text-lg font-semibold">Install recipes</h2>
      <p className="text-sm text-muted-foreground">Pick your stack. Every snippet already contains this site&apos;s ID and host.</p>
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
