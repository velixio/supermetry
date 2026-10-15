import { useEffect, useState } from 'react'
import { CheckIcon, CircleIcon, LoaderCircleIcon, WifiOffIcon } from 'lucide-react'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { en } from '@/messages/en'
import type { AdminUpdateJob } from './adminApi'

type StepKey = keyof typeof en.admin.updateSteps

// Typical seconds per step, used only for the time estimate. Stopping is the first step during
// which Supermetry is unreachable.
export const updateSteps: ReadonlyArray<{ key: StepKey; seconds: number }> = [
  { key: 'preflight', seconds: 2 },
  { key: 'downloading', seconds: 15 },
  { key: 'verifying', seconds: 2 },
  { key: 'backing_up', seconds: 20 },
  { key: 'stopping', seconds: 2 },
  { key: 'activating', seconds: 1 },
  { key: 'restarting', seconds: 3 },
  { key: 'verifying_health', seconds: 3 },
]

const offlineFrom = updateSteps.findIndex((step) => step.key === 'stopping')

export function stepIndex(status: AdminUpdateJob['status']): number {
  const found = updateSteps.findIndex((step) => step.key === status)
  if (found >= 0) return found
  return status === 'complete' ? updateSteps.length : 0
}

export function estimate(index: number, elapsedSeconds: number) {
  const current = updateSteps[index]
  const later = updateSteps.slice(index + 1).reduce((sum, step) => sum + step.seconds, 0)
  const total = updateSteps.reduce((sum, step) => sum + step.seconds, 0)
  if (!current) return { remaining: 0, percent: 100, overdue: false }
  const elapsed = Math.max(0, elapsedSeconds)
  const done = updateSteps.slice(0, index).reduce((sum, step) => sum + step.seconds, 0)
  return {
    remaining: Math.ceil(Math.max(0, current.seconds - elapsed) + later),
    percent: Math.min(99, Math.round(((done + Math.min(elapsed, current.seconds * 0.9)) / total) * 100)),
    overdue: elapsed > current.seconds,
  }
}

function useNow(active: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [active])
  return now
}

export function UpdateProgress({ job, reconnecting }: { job: AdminUpdateJob; reconnecting: boolean }) {
  const now = useNow(true)
  const index = stepIndex(job.status)
  const elapsed = (now - Date.parse(job.updated_at)) / 1000
  const { remaining, percent, overdue } = estimate(index, Number.isFinite(elapsed) ? elapsed : 0)
  const offline = index >= offlineFrom

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-2">
        <div role="progressbar" aria-label={en.admin.updateProgress} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} className="h-2 w-full overflow-hidden rounded-full bg-muted">
          <div className="h-full rounded-full bg-primary transition-[width] duration-500" style={{ width: `${percent}%` }} />
        </div>
        <p className="text-sm text-muted-foreground">
          {overdue ? en.admin.updateEtaLonger : en.admin.updateEta.replace('{seconds}', String(remaining))}
        </p>
      </div>
      {offline ? (
        <Alert>
          <WifiOffIcon aria-hidden="true" />
          <AlertTitle>{reconnecting ? en.admin.updateReconnecting : en.admin.updateOfflineTitle}</AlertTitle>
          <AlertDescription>{en.admin.updateOfflineDescription}</AlertDescription>
        </Alert>
      ) : (
        <Alert>
          <WifiOffIcon aria-hidden="true" />
          <AlertTitle>{en.admin.offlineWarningTitle}</AlertTitle>
          <AlertDescription>{en.admin.offlineWarningDescription}</AlertDescription>
        </Alert>
      )}
      <ol aria-label={en.admin.updateStepsLabel} className="flex flex-col gap-2">
        {updateSteps.map((step, position) => {
          const state = position < index ? 'done' : position === index ? 'active' : 'pending'
          return (
            <li key={step.key} aria-current={state === 'active' ? 'step' : undefined} className={`flex items-center gap-2 text-sm ${state === 'pending' ? 'text-muted-foreground' : ''}`}>
              {state === 'done' ? <CheckIcon aria-hidden="true" className="size-4 text-primary" /> : state === 'active' ? <LoaderCircleIcon aria-hidden="true" className="size-4 animate-spin" /> : <CircleIcon aria-hidden="true" className="size-4" />}
              <span>{en.admin.updateSteps[step.key]}</span>
              <span className="sr-only">{state === 'done' ? en.admin.stepDone : state === 'active' ? en.admin.stepActive : en.admin.stepPending}</span>
            </li>
          )
        })}
      </ol>
    </div>
  )
}
