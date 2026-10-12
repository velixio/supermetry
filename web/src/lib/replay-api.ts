import { api } from './api'

import type { components } from './api.gen'

export type SiteSettings = components['schemas']['ReplaySiteSettings']
export type ReplayListItem = Omit<components['schemas']['ReplaySummary'], 'session_id'> & {
  session_id: string
}
export type ReplayList = Omit<components['schemas']['ReplayList'], 'data'> & { items: ReplayListItem[] }
export type ReplayListQuery = {
  from: string
  to: string
  q?: string
  min_duration?: number
  max_duration?: number
  has_events?: boolean
  saved?: boolean
  cursor?: string
  limit?: number
}
type GeneratedReplayStorageSettings = components['schemas']['ReplayStorageSettings']
export type ReplayStorageSettings = Omit<GeneratedReplayStorageSettings, 'backend'> & {
  backend: Omit<GeneratedReplayStorageSettings['backend'], 'value'> & { value: 'disk' | 's3' }
}

export class ReplayApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message)
  }
}

function resultError(result: { error?: unknown; response: Response }): ReplayApiError {
  const detail = (result.error as { detail?: string } | undefined)?.detail
  return new ReplayApiError(result.response.status, detail ?? `Request failed (${result.response.status})`)
}

function requireData<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (!result.response.ok) {
    throw resultError(result)
  }
  if (result.data === undefined) throw new ReplayApiError(result.response.status, 'The replay response was empty.')
  return result.data
}

// Older servers encode an empty pattern list as null; the form needs an array.
export function normalizeReplaySettings(settings: SiteSettings): SiteSettings {
  return { ...settings, triggers: { ...settings.triggers, url_patterns: settings.triggers.url_patterns ?? [] } }
}

export async function getReplaySettings(siteId: number, signal?: AbortSignal): Promise<SiteSettings> {
  return normalizeReplaySettings(requireData(await api.GET('/api/private/sites/{site_id}/replay-settings', {
    params: { path: { site_id: siteId } }, signal,
  })))
}

export async function putReplaySettings(siteId: number, settings: SiteSettings): Promise<SiteSettings> {
  return normalizeReplaySettings(requireData(await api.PUT('/api/private/sites/{site_id}/replay-settings', {
    params: { path: { site_id: siteId } },
    body: replaySettingsBody(settings),
  })))
}

// Submit the project opt-in and privacy constraints; the server rejects any
// attempted escalation beyond the instance policy.
export function replaySettingsBody(settings: SiteSettings) {
  return {
    ...(settings.configured_enabled === undefined ? {} : { enabled: settings.configured_enabled }),
    ...(settings.mask.locked ? {} : { mask: settings.mask.value }),
    sample_rate: settings.sample_rate.value,
    retention_days: settings.retention_days.value,
    quota_bytes: settings.quota_bytes.value,
    triggers: settings.triggers,
    consent: settings.consent,
  }
}

export async function listReplays(
  siteId: number,
  query: ReplayListQuery,
  signal?: AbortSignal,
): Promise<ReplayList> {
  const data = requireData(await api.GET('/api/private/sites/{site_id}/replays', {
    params: { path: { site_id: siteId }, query }, signal,
  }))
  return { ...data, items: data.data.map((item) => ({ ...item, session_id: String(item.session_id) })) }
}

export async function setReplaySaved(siteId: number, sessionId: string, saved: boolean): Promise<void> {
  const params = { path: { site_id: siteId, session_id: sessionId } }
  const result = saved
    ? await api.PUT('/api/private/sites/{site_id}/replays/{session_id}/saved', { params })
    : await api.DELETE('/api/private/sites/{site_id}/replays/{session_id}/saved', { params })
  if (!result.response.ok) throw resultError(result)
}

export async function getReplayStorage(signal?: AbortSignal): Promise<ReplayStorageSettings> {
  return requireData(await api.GET('/api/private/instance/replay-storage', { signal })) as ReplayStorageSettings
}

export async function putReplayStorage(settings: ReplayStorageSettings, migrate: boolean): Promise<ReplayStorageSettings> {
  return requireData(await api.PUT('/api/private/instance/replay-storage', {
    body: {
      backend: settings.backend.value,
      dir: settings.dir.value,
      s3: {
        endpoint: settings.s3.value.endpoint,
        bucket: settings.s3.value.bucket,
        region: settings.s3.value.region,
        access_key: settings.s3.value.access_key,
        prefix: settings.s3.value.prefix,
      },
      migrate,
    },
  })) as ReplayStorageSettings
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
  const result = await api.GET('/api/private/sites/{site_id}/replays/{session_id}/data', {
    params: { path: { site_id: siteId, session_id: sessionId } }, signal, parseAs: 'text',
  })
  return parseReplayNDJSON(requireData(result))
}

export async function getReplayCSS(siteId: number, hash: string, signal?: AbortSignal): Promise<string> {
  return requireData(await api.GET('/api/private/sites/{site_id}/replays/css/{hash}', {
    params: { path: { site_id: siteId, hash } }, signal, parseAs: 'text',
  }))
}
