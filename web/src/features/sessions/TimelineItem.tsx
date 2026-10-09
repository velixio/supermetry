import { Badge } from '@/components/ui/badge'

import type { SessionTimelineItem } from './types'

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

export function TimelineItem({ item, onClick }: { item: SessionTimelineItem; onClick?: () => void }) {
  const props = item.props ? Object.entries(item.props) : []
  const content = (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={item.kind === 'pageview' ? 'secondary' : 'outline'}>{item.kind}</Badge>
        <span className="font-medium">{item.name}</span>
        <time className="text-xs text-muted-foreground" dateTime={item.ts}>{formatDate(item.ts)}</time>
      </div>
      <span className="text-sm text-muted-foreground">{item.page}</span>
      {props.map(([key, value]) => (
        <span className="text-xs text-muted-foreground" key={key}>{key}: {String(value)}</span>
      ))}
    </>
  )
  return (
    <li className="flex flex-col gap-1 border-l-2 border-border py-1 pl-4">
      {onClick ? (
        <button className="flex flex-col gap-1 rounded-md text-left outline-none focus-visible:ring-2 focus-visible:ring-ring" type="button" onClick={onClick}>
          {content}
        </button>
      ) : content}
    </li>
  )
}
