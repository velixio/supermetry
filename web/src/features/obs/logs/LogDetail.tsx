import { obsLogs as t } from '@/messages/obsLogs'

import type { ObsLog } from './api'

/** The expanded part of a log row: ids, release, environment and every attribute. */
export function LogDetail({ log }: { log: ObsLog }) {
  const attributes = Object.entries(log.attributes ?? {}).sort(([a], [b]) => a.localeCompare(b))
  const facts: Array<[string, string]> = [
    ['environment', log.environment], ['release', log.release], ['trace_id', log.trace_id ?? ''], ['span_id', log.span_id ?? ''],
  ]
  return (
    <div className="flex flex-col gap-2 bg-muted/40 px-9 py-3 text-xs">
      {log.truncated ? <p className="text-muted-foreground">{t.truncated}</p> : null}
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
        {facts.filter(([, value]) => value).map(([key, value]) => (
          <div key={key} className="contents"><dt className="text-muted-foreground">{key}</dt><dd className="font-mono break-all">{value}</dd></div>
        ))}
      </dl>
      <h4 className="font-medium">{t.attributes}</h4>
      {attributes.length === 0 ? <p className="text-muted-foreground">{t.noAttributes}</p> : (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
          {attributes.map(([key, value]) => (
            <div key={key} className="contents"><dt className="text-muted-foreground">{key}</dt><dd className="font-mono break-all">{typeof value === 'string' ? value : JSON.stringify(value)}</dd></div>
          ))}
        </dl>
      )}
    </div>
  )
}
