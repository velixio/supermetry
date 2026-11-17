/** Tolerant view of the scrubbed Sentry-shaped body: every field may be missing. */
export type BodyFrame = { filename?: string; function?: string; module?: string; lineno?: number; colno?: number; in_app?: boolean; context_line?: string; pre_context?: string[]; post_context?: string[] }
export type BodyException = { type?: string; value?: string; mechanism?: { type?: string; handled?: boolean }; frames?: BodyFrame[] }
export type BodyBreadcrumb = { timestamp?: string | number; type?: string; category?: string; level?: string; message?: string; data?: Record<string, unknown> }
export type BodyAttachment = { name?: string; type?: string; size?: number }
export type EventBodyView = {
  message?: string
  exceptions?: BodyException[]
  breadcrumbs?: BodyBreadcrumb[]
  contexts?: Record<string, Record<string, unknown> | undefined>
  request?: { method?: string; url?: string; headers?: Record<string, string> }
  tags?: Record<string, unknown>
  attachments?: BodyAttachment[]
  sdk?: { name?: string; version?: string }
}

const asArray = <T,>(value: unknown): T[] => (Array.isArray(value) ? (value as T[]) : [])
const asRecord = (value: unknown): Record<string, unknown> => (value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : {})

/** Normalise an unknown body into arrays and records so the view never branches on undefined. */
export function readBody(raw: unknown) {
  const body = asRecord(raw) as EventBodyView
  return {
    message: typeof body.message === 'string' ? body.message : '',
    exceptions: asArray<BodyException>(body.exceptions).map((exception) => ({ ...exception, frames: asArray<BodyFrame>(exception?.frames) })),
    breadcrumbs: asArray<BodyBreadcrumb>(body.breadcrumbs),
    contexts: Object.entries(asRecord(body.contexts)).filter(([, value]) => value && typeof value === 'object') as [string, Record<string, unknown>][],
    request: body.request && typeof body.request === 'object' ? body.request : undefined,
    tags: Object.entries(asRecord(body.tags)),
    attachments: asArray<BodyAttachment>(body.attachments),
    sdk: body.sdk && typeof body.sdk === 'object' ? body.sdk : undefined,
  }
}

export function frameLabel(frame: BodyFrame): string {
  const where = frame.filename || frame.module || '<unknown>'
  const position = frame.lineno ? `:${frame.lineno}${frame.colno ? `:${frame.colno}` : ''}` : ''
  return `${where}${position}`
}

export function crumbTime(value: string | number | undefined): string {
  if (value === undefined || value === '') return ''
  const date = new Date(typeof value === 'number' ? value * 1000 : value)
  return Number.isNaN(date.getTime()) ? String(value) : date.toISOString().slice(11, 19)
}

export function show(value: unknown): string {
  if (typeof value === 'string') return value
  if (value === null || value === undefined) return ''
  return typeof value === 'object' ? JSON.stringify(value) : String(value)
}
