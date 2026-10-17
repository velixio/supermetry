import type { ReactNode } from 'react'
import { CheckIcon } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { activeLocale } from '@/lib/i18n/intl'
import { uiFlows } from '@/messages/uiFlows'

export type InstallStatus = 'waiting' | 'receiving' | 'quiet'

function relativeTime(value: string, now: Date): string {
  const seconds = Math.round((new Date(value).getTime() - now.getTime()) / 1_000)
  const absolute = Math.abs(seconds)
  const formatter = new Intl.RelativeTimeFormat(activeLocale(), { numeric: 'always' })
  if (absolute < 60) return formatter.format(seconds, 'second')
  const minutes = Math.round(seconds / 60)
  if (Math.abs(minutes) < 60) return formatter.format(minutes, 'minute')
  const hours = Math.round(minutes / 60)
  if (Math.abs(hours) < 24) return formatter.format(hours, 'hour')
  return formatter.format(Math.round(hours / 24), 'day')
}

const labels: Record<InstallStatus, string> = {
  waiting: 'Waiting',
  receiving: 'Receiving',
  quiet: 'Quiet',
}

export function InstallStatusView({
  status,
  lastSeenAt,
  now = new Date(),
}: {
  status?: InstallStatus
  lastSeenAt?: string | null
  now?: Date
}) {
  const resolved = status ?? (lastSeenAt ? 'receiving' : 'waiting')
  const detail = lastSeenAt
    ? `Last event ${relativeTime(lastSeenAt, now)}`
    : 'Waiting for the first accepted event.'
  return (
    <div className="flex flex-wrap items-center gap-2" role="status">
      <Badge variant={resolved === 'receiving' ? 'default' : resolved === 'quiet' ? 'secondary' : 'outline'}>
        {labels[resolved]}
      </Badge>
      <span className="text-xs text-muted-foreground">{detail}</span>
    </div>
  )
}

/** The common reasons a freshly installed snippet sends nothing, with a link to the domain list. */
export function InstallTroubleshooting({ domainsHref, renderLink, open = false }: {
  domainsHref?: string
  /** Renders an in-app link; plain text when absent. */
  renderLink?: (href: string, label: string) => ReactNode
  open?: boolean
}) {
  return (
    <details className="rounded-lg border bg-background px-3 py-2 text-sm" open={open}>
      <summary className="cursor-pointer font-medium select-none">{uiFlows.install.troubleshootTitle}</summary>
      <ul className="mt-2 flex list-disc flex-col gap-1.5 pl-5 text-muted-foreground">
        <li>
          {uiFlows.install.tipDomains}{' '}
          {domainsHref && renderLink ? renderLink(domainsHref, uiFlows.install.tipDomainsLink) : null}
        </li>
        <li>{uiFlows.install.tipBlocker}</li>
        <li>{uiFlows.install.tipCsp}</li>
        <li>{uiFlows.install.tipCache}</li>
      </ul>
    </details>
  )
}

type Step = { label: string; state: 'done' | 'current' | 'todo' }

/**
 * The live first-run status at the top of the installation page: a three-step progress
 * (create, install, first event) and what to do next, with the dashboard one click away
 * once events arrive.
 */
export function InstallStatusCard({ status, lastSeenAt, now = new Date(), dashboardLink, troubleshooting }: {
  status?: InstallStatus
  lastSeenAt?: string | null
  now?: Date
  dashboardLink?: ReactNode
  troubleshooting?: ReactNode
}) {
  const resolved = status ?? (lastSeenAt ? 'receiving' : 'waiting')
  const steps: Step[] = [
    { label: uiFlows.install.stepCreate, state: 'done' },
    { label: uiFlows.install.stepInstall, state: resolved === 'waiting' ? 'current' : 'done' },
    { label: uiFlows.install.stepVerify, state: resolved === 'waiting' ? 'todo' : 'done' },
  ]
  const title = resolved === 'receiving' ? uiFlows.install.receivingTitle : resolved === 'quiet' ? uiFlows.install.quietTitle : uiFlows.install.waitingTitle
  const body = resolved === 'receiving' ? uiFlows.install.receivingBody : resolved === 'quiet' ? uiFlows.install.quietBody : uiFlows.install.waitingBody
  return (
    <section aria-labelledby="install-status-title" className="flex flex-col gap-4 rounded-xl border bg-muted/30 p-4">
      <ol aria-label={uiFlows.install.steps} className="flex flex-wrap gap-x-4 gap-y-2 text-sm">
        {steps.map((step, index) => (
          <li key={step.label} className="flex items-center gap-2" aria-current={step.state === 'current' ? 'step' : undefined}>
            <span
              aria-hidden="true"
              className={
                step.state === 'done'
                  ? 'grid size-6 place-items-center rounded-full bg-primary text-primary-foreground'
                  : step.state === 'current'
                    ? 'grid size-6 place-items-center rounded-full border-2 border-primary text-primary'
                    : 'grid size-6 place-items-center rounded-full border text-muted-foreground'
              }
            >
              {step.state === 'done' ? <CheckIcon className="size-3.5" /> : index + 1}
            </span>
            <span className={step.state === 'todo' ? 'text-muted-foreground' : 'font-medium'}>{step.label}</span>
            <span className="sr-only">{step.state === 'done' ? `(${uiFlows.install.stepDone})` : step.state === 'current' ? `(${uiFlows.install.stepCurrent})` : ''}</span>
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-1 items-start gap-3">
          <span aria-hidden="true" className="relative mt-1.5 flex size-2.5 shrink-0">
            {resolved === 'waiting' ? <span className="absolute inline-flex size-full rounded-full bg-amber-500 opacity-75 motion-safe:animate-ping" /> : null}
            <span className={`relative inline-flex size-2.5 rounded-full ${resolved === 'receiving' ? 'bg-emerald-500' : resolved === 'quiet' ? 'bg-muted-foreground' : 'bg-amber-500'}`} />
          </span>
          <div className="flex min-w-0 flex-col gap-1">
            <h2 id="install-status-title" className="text-base font-semibold">{title}</h2>
            <p className="text-sm text-muted-foreground">{body}</p>
            <InstallStatusView status={status} lastSeenAt={lastSeenAt} now={now} />
          </div>
        </div>
        {resolved !== 'waiting' && dashboardLink ? dashboardLink : null}
      </div>
      {resolved !== 'receiving' ? troubleshooting : null}
    </section>
  )
}
