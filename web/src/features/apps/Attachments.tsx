import { useEffect, useState } from 'react'

import { InfoTip } from '@/components/InfoTip'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

import type { BodyAttachment } from './eventBody'

export type AttachmentSource = { siteId: number; eventId: string }

const imageTypes = new Set(['image/png', 'image/jpeg', 'image/webp'])
const textTypes = new Set(['text/plain', 'application/json'])

/** Only these image types are ever shown in the page; the server enforces the same list. */
export const isInlineImage = (contentType?: string): boolean => !!contentType && imageTypes.has(contentType)

export function attachmentURL(siteId: number, eventId: string, index: number): string {
  return `/api/private/sites/${siteId}/app/events/${encodeURIComponent(eventId)}/attachments/${index}`
}

function formatSize(bytes?: number): string {
  if (bytes === undefined) return ''
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

/** Text arrives scrubbed and is shown as text nodes only, never as markup. */
function TextViewer({ url, name }: { url: string; name: string }) {
  const [state, setState] = useState<{ status: 'loading' } | { status: 'error' } | { status: 'ready'; text: string }>({ status: 'loading' })
  useEffect(() => {
    const controller = new AbortController()
    fetch(url, { credentials: 'same-origin', signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(String(response.status))
        setState({ status: 'ready', text: await response.text() })
      })
      .catch(() => {
        if (!controller.signal.aborted) setState({ status: 'error' })
      })
    return () => controller.abort()
  }, [url])
  if (state.status === 'loading') return <p role="status" className="text-xs text-muted-foreground">Loading {name}…</p>
  if (state.status === 'error') return <p role="alert" className="text-xs text-destructive">Could not load {name}.</p>
  return <pre aria-label={`Contents of ${name}`} className="max-h-80 overflow-auto rounded-md bg-muted p-3 font-mono text-xs whitespace-pre-wrap break-words">{state.text}</pre>
}

function AttachmentRow({ attachment, index, source }: { attachment: BodyAttachment; index: number; source: AttachmentSource }) {
  const [open, setOpen] = useState(false)
  const name = attachment.name || 'attachment'
  const size = formatSize(attachment.size)
  if (attachment.stored !== true) {
    return (
      <li className="flex flex-wrap items-center gap-2 py-2 text-sm">
        <span className="font-medium">{name}</span>
        {size ? <span className="text-muted-foreground">{size}</span> : null}
        <Badge variant="outline">Not stored</Badge>
      </li>
    )
  }
  const url = attachmentURL(source.siteId, source.eventId, index)
  const image = isInlineImage(attachment.content_type)
  const text = !!attachment.content_type && textTypes.has(attachment.content_type)
  return (
    <li className="flex flex-col gap-2 py-2 text-sm">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="font-medium">{name}</span>
        {size ? <span className="text-muted-foreground">{size}</span> : null}
        {text ? <Button type="button" variant="link" size="sm" className="h-auto p-0" aria-expanded={open} onClick={() => setOpen(!open)}>{open ? `Hide ${name}` : `View ${name}`}</Button> : null}
        <a className="font-medium underline" href={url} download={name} aria-label={`Download ${name}`}>Download</a>
      </div>
      {image ? (
        <a href={url} target="_blank" rel="noopener noreferrer" className="w-fit">
          <img src={url} alt={name} loading="lazy" className="max-h-40 max-w-full rounded-md object-contain" />
        </a>
      ) : null}
      {text && open ? <TextViewer url={url} name={name} /> : null}
    </li>
  )
}

export function Attachments({ attachments, source }: { attachments: BodyAttachment[]; source?: AttachmentSource }) {
  if (attachments.length === 0) return null
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Attachments <InfoTip label="About attachments">Screenshots and text files sent with the event. Text is scrubbed of personal data; files over 1 MiB, other types and anything past 5 MiB per event are not stored.</InfoTip></CardTitle>
      </CardHeader>
      <CardContent>
        <ul className="flex flex-col" aria-label="Attachments">
          {attachments.map((attachment, i) => (
            source ? <AttachmentRow key={i} attachment={attachment} index={i} source={source} /> : (
              <li key={i} className="py-2 text-sm">{attachment.name || 'attachment'} {attachment.size !== undefined ? <span className="text-muted-foreground">{formatSize(attachment.size)}</span> : null}</li>
            )
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
