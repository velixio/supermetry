import { en } from '@/messages/en'

type EventExportSource = 'mixpanel' | 'amplitude'

const t = en.eventExportImport

export function isEventExportSource(source: string): source is EventExportSource {
  return source === 'mixpanel' || source === 'amplitude'
}

export const eventExportAccept: Record<EventExportSource, string> = {
  mixpanel: '.json,.jsonl,.ndjson,.csv,.gz,.zip',
  amplitude: '.json,.jsonl,.ndjson,.gz,.zip',
}

export function EventExportFormat({ source }: { source: EventExportSource }) {
  const copy = t[source]
  return (
    <div className="flex flex-col gap-2 rounded-lg border bg-muted/40 p-4 text-sm">
      <h3 className="font-medium">{t.formatTitle}</h3>
      <ul className="list-disc space-y-1 pl-5 text-muted-foreground">
        <li>{copy.file}</li>
        <li>{copy.fields}</li>
        <li>{t.formatTime}</li>
        <li>{t.formatPrivacy}</li>
        <li>{t.formatSessions}</li>
      </ul>
    </div>
  )
}
