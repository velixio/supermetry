import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { copyText } from '@/lib/clipboard'

import { useAppOverview } from './api'

const POLL_MS = 5000
const GIVE_UP_MS = 120_000

/** One `kind: "error"` report, ready to paste into a terminal; the key stays a placeholder. */
export function testReportSnippet(origin: string, appId: string): string {
  const body = JSON.stringify({ site: appId, events: [{ id: 'setup-check-1', kind: 'error', release: '0.0.0-test', platform: 'other', error_type: 'SetupCheck', frames: 'setup check' }] })
  return `curl --request POST ${origin}/api/public/v1/app-events \\\n  --header 'Content-Type: application/json' \\\n  --header 'X-Supermetry-Key: <your key>' \\\n  --data '${body}'`
}

/** Three-step setup checklist; the last step polls the overview until the first report arrives. */
export function TestReportCheck({ appId, siteId, timezone, origin = window.location.origin }: { appId: string; siteId: number; timezone: string; origin?: string }) {
  const [attempt, setAttempt] = useState(0)
  const [timedOut, setTimedOut] = useState(false)
  const [received, setReceived] = useState(false)
  const active = !received && !timedOut
  const overview = useAppOverview(siteId, { period: '7d' }, timezone, { refetchInterval: active ? POLL_MS : false })
  const events = overview.data?.events ?? 0
  useEffect(() => { if (events > 0) setReceived(true) }, [events])
  useEffect(() => {
    if (received) return undefined
    const timer = setTimeout(() => setTimedOut(true), GIVE_UP_MS)
    return () => clearTimeout(timer)
  }, [received, attempt])
  const snippet = testReportSnippet(origin, appId)
  const state = received ? 'received' : timedOut ? 'timed-out' : 'waiting'
  return (
    <ol className="flex flex-col gap-3 text-sm" aria-label="Setup steps">
      <li><strong>1. Add the key to your build.</strong> Keep it in your app's build configuration.</li>
      <li className="flex flex-col gap-2">
        <span><strong>2. Send a test report.</strong> Replace the placeholder with your key and run it:</span>
        <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs"><code>{snippet}</code></pre>
        <div><Button type="button" size="sm" variant="outline" onClick={() => void copyText(snippet)}>Copy command</Button></div>
      </li>
      <li className="flex flex-wrap items-center gap-2">
        <strong>3. Check it arrived.</strong>
        <span role="status" data-state={state} className={received ? 'font-medium text-chart-2' : 'text-muted-foreground'}>
          {received ? 'First report received' : timedOut ? 'No report yet. Check the key and the command.' : 'Waiting for first report…'}
        </span>
        {timedOut ? <Button type="button" size="sm" variant="outline" onClick={() => { setTimedOut(false); setAttempt(attempt + 1) }}>Check again</Button> : null}
      </li>
    </ol>
  )
}
