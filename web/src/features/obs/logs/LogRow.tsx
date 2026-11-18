import { ChevronDownIcon, ChevronRightIcon } from 'lucide-react'
import { useState } from 'react'
import { Link } from '@tanstack/react-router'

import { obsLogs as t } from '@/messages/obsLogs'

import type { ObsLog } from './api'
import { LogDetail } from './LogDetail'

const tones: Record<string, string> = {
  fatal: 'bg-destructive text-white',
  error: 'bg-destructive/15 text-destructive',
  warn: 'bg-amber-500/15 text-amber-700 dark:text-amber-400',
  info: 'bg-sky-500/15 text-sky-700 dark:text-sky-400',
  debug: 'bg-muted text-muted-foreground',
  trace: 'bg-muted text-muted-foreground',
}

/** The log time in the site's time zone, to the millisecond. */
export function formatLogTime(iso: string, timezone: string): string {
  try {
    const parts = new Intl.DateTimeFormat('en-CA', { timeZone: timezone || 'UTC', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).format(new Date(iso))
    return `${parts.replace(',', '')}.${String(new Date(iso).getUTCMilliseconds()).padStart(3, '0')}`
  } catch {
    return iso
  }
}

export function SeverityBadge({ log }: { log: ObsLog }) {
  const label = log.severity_text || 'unset'
  return <span className={`inline-flex w-14 justify-center rounded px-1.5 py-0.5 text-xs font-medium ${tones[log.severity_text] ?? 'bg-muted text-muted-foreground'}`}>{label}</span>
}

export function LogRow({ log, org, site, timezone, showTraceLink = true }: { log: ObsLog; org: string; site: string; timezone: string; showTraceLink?: boolean }) {
  const [open, setOpen] = useState(false)
  const Chevron = open ? ChevronDownIcon : ChevronRightIcon
  return (
    <li className="border-b last:border-b-0">
      <div className="flex flex-wrap items-start gap-x-3 gap-y-1 px-3 py-2 text-sm">
        <button type="button" aria-expanded={open} aria-label={open ? t.collapse : t.expand} className="mt-0.5 shrink-0 text-muted-foreground" onClick={() => setOpen(!open)}>
          <Chevron className="size-4" />
        </button>
        <time className="shrink-0 font-mono text-xs text-muted-foreground" dateTime={log.ts}>{formatLogTime(log.ts, timezone)}</time>
        <SeverityBadge log={log} />
        {log.service ? <span className="shrink-0 text-xs text-muted-foreground">{log.service}</span> : null}
        <span className="min-w-0 flex-1 break-words font-mono text-xs">{log.body}</span>
        {showTraceLink && log.trace_id ? (
          <Link className="shrink-0 text-xs font-medium underline" to="/app/$org/$site/traces/$trace" params={{ org, site, trace: log.trace_id }}>{t.viewTrace}</Link>
        ) : null}
      </div>
      {open ? <LogDetail log={log} /> : null}
    </li>
  )
}
