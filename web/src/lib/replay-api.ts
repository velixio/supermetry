import { api } from './api'

export type LockedValue<T> = { value: T; locked: boolean }

export type ReplayTriggers = {
  error: boolean
  rage_click: boolean
  url: boolean
  manual: boolean
  url_patterns: string[]
}

export type SiteSettings = {
  enabled: boolean
  mask: LockedValue<boolean>
  sample_rate: LockedValue<number>
  retention_days: LockedValue<number>
  quota_bytes: LockedValue<number>
  triggers: ReplayTriggers
  consent: 'auto' | 'always' | 'never'
}

export type ReplayListItem = {
  session_id: string
  started_at: string
  duration_s: number
  pages: number
  bytes: number
  trigger: string
  profile_id?: number | null
}

export type ReplayList = { items: ReplayListItem[]; next_cursor?: string | null }

export type S3StorageValue = {
  endpoint: string
  bucket: string
  region: string
  access_key: string
  prefix: string
  secret_key_set: boolean
}

export type ReplayStorageSettings = {
  backend: LockedValue<'disk' | 's3'>
  dir: LockedValue<string>
  s3: LockedValue<S3StorageValue>
}

export class ReplayApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message)
  }
}

type Method = 'GET' | 'PUT'
type ApiResult = { data?: unknown; error?: unknown; response: Response }
type LooseClient = Record<Method, (path: string, options: Record<string, unknown>) => Promise<ApiResult>>

async function request<T>(method: Method, path: string, options: Record<string, unknown> = {}): Promise<T> {
  const result = await (api as unknown as LooseClient)[method](path, options)
  if (!result.response.ok) {
    const detail = (result.error as { detail?: string } | undefined)?.detail
    throw new ReplayApiError(result.response.status, detail ?? `Request failed (${result.response.status})`)
  }
  if (result.data === undefined) throw new ReplayApiError(result.response.status, 'The replay response was empty.')
  return result.data as T
}

export function getReplaySettings(siteId: number, signal?: AbortSignal): Promise<SiteSettings> {
  return request('GET', `/api/private/sites/${siteId}/replay-settings`, { signal })
}

export function putReplaySettings(siteId: number, settings: SiteSettings): Promise<SiteSettings> {
  return request('PUT', `/api/private/sites/${siteId}/replay-settings`, { body: settings })
}

export function listReplays(
  siteId: number,
  query: { from: string; to: string; cursor?: string; limit?: number },
  signal?: AbortSignal,
): Promise<ReplayList> {
  return request('GET', `/api/private/sites/${siteId}/replays`, { params: { query }, signal })
}

export function getReplayStorage(signal?: AbortSignal): Promise<ReplayStorageSettings> {
  return request('GET', '/api/private/instance/replay-storage', { signal })
}

export function putReplayStorage(settings: ReplayStorageSettings, migrate: boolean): Promise<ReplayStorageSettings> {
  return request('PUT', '/api/private/instance/replay-storage', { body: { ...settings, migrate } })
}

export type ReplayData = { events: unknown[]; truncated: boolean }

export function parseReplayNDJSON(value: string): ReplayData {
  const lines = value.split('\n')
  let lastContent = lines.length - 1
  while (lastContent >= 0 && lines[lastContent].trim() === '') lastContent -= 1
  const events: unknown[] = []
  for (let index = 0; index <= lastContent; index += 1) {
    const line = lines[index].trim()
    if (!line) continue
    try {
      events.push(JSON.parse(line) as unknown)
    } catch {
      if (index === lastContent) return { events, truncated: true }
      throw new Error(`Invalid replay event on line ${index + 1}`)
    }
  }
  return { events, truncated: false }
}

export async function getReplayData(siteId: number, sessionId: string, signal?: AbortSignal): Promise<ReplayData> {
  const response = await fetch(`/api/private/sites/${siteId}/replays/${encodeURIComponent(sessionId)}/data`, {
    credentials: 'same-origin',
    signal,
  })
  if (!response.ok) throw new ReplayApiError(response.status, `Replay data request failed (${response.status})`)
  return parseReplayNDJSON(await response.text())
}
