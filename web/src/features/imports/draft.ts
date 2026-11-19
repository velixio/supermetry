import type { ImportSource } from '@/features/settings/privateApi'

import type { PullBody, PullMethod } from './api'

export type WizardStep = 'source' | 'range' | 'preview' | 'run' | 'review'
export const wizardSteps: WizardStep[] = ['source', 'range', 'preview', 'run', 'review']

export const MAX_PREVIEW_DAYS = 120
export const MAX_PULL_DAYS = 3660

/** What the user has chosen so far. Secrets live only in memory (never persisted). */
export type Draft = {
  mode: 'file' | 'api'
  fileSource: ImportSource
  file?: File
  apiMethod: PullMethod
  baseUrl: string
  remoteSiteId: string
  apiKey: string
  tokenAuth: string
  connectionId: number
  property: string
  from: string
  to: string
}

export function isoDay(date: Date): string {
  return date.toISOString().slice(0, 10)
}

export function addDays(day: string, delta: number): string {
  const date = new Date(`${day}T00:00:00Z`)
  date.setUTCDate(date.getUTCDate() + delta)
  return isoDay(date)
}

/** Inclusive number of days from a to b; 0 when either date is invalid. */
export function dayCount(from: string, to: string): number {
  const a = Date.parse(`${from}T00:00:00Z`)
  const b = Date.parse(`${to}T00:00:00Z`)
  if (Number.isNaN(a) || Number.isNaN(b) || b < a) return 0
  return Math.round((b - a) / 86_400_000) + 1
}

export function defaultDraft(now: Date = new Date()): Draft {
  const to = addDays(isoDay(now), -1)
  return {
    mode: 'api', fileSource: 'plausible', apiMethod: 'plausible_api',
    baseUrl: 'https://plausible.io', remoteSiteId: '', apiKey: '', tokenAuth: '',
    connectionId: 0, property: '', from: addDays(to, -89), to,
  }
}

export type RangeProblem = 'invalid' | 'tooLong' | 'future' | null

export function rangeProblem(draft: Pick<Draft, 'from' | 'to'>, now: Date = new Date()): RangeProblem {
  const days = dayCount(draft.from, draft.to)
  if (days === 0) return 'invalid'
  if (days > MAX_PULL_DAYS) return 'tooLong'
  if (draft.to > isoDay(now)) return 'future'
  return null
}

/** Is the source step complete enough to continue? */
export function sourceReady(draft: Draft): boolean {
  if (draft.mode === 'file') return draft.file !== undefined
  switch (draft.apiMethod) {
    case 'plausible_api':
      return draft.baseUrl.trim() !== '' && draft.remoteSiteId.trim() !== '' && draft.apiKey.trim() !== ''
    case 'matomo_api':
      return draft.baseUrl.trim() !== '' && /^[0-9]+$/.test(draft.remoteSiteId.trim()) && draft.tokenAuth.trim() !== ''
    case 'ga4_api':
      return draft.connectionId > 0 && draft.property !== ''
  }
}

export function pullBody(draft: Draft, range: { from: string; to: string } = draft): PullBody {
  const body: PullBody = { method: draft.apiMethod, from: range.from, to: range.to }
  if (draft.apiMethod === 'plausible_api') {
    Object.assign(body, { base_url: draft.baseUrl.trim(), remote_site_id: draft.remoteSiteId.trim(), api_key: draft.apiKey.trim() })
  } else if (draft.apiMethod === 'matomo_api') {
    Object.assign(body, { base_url: draft.baseUrl.trim(), remote_site_id: draft.remoteSiteId.trim(), token_auth: draft.tokenAuth.trim() })
  } else {
    Object.assign(body, { property: draft.property, connection_id: draft.connectionId })
  }
  return body
}

/** A dry run fetches synchronously, so long ranges are previewed on their most recent days. */
export function previewRange(draft: Pick<Draft, 'from' | 'to'>): { from: string; to: string; partial: boolean; total: number } {
  const total = dayCount(draft.from, draft.to)
  if (total <= MAX_PREVIEW_DAYS) return { from: draft.from, to: draft.to, partial: false, total }
  return { from: addDays(draft.to, -(MAX_PREVIEW_DAYS - 1)), to: draft.to, partial: true, total }
}

const storageKey = (siteId: number) => `supermetry:import-draft:${siteId}`

type Persisted = Pick<Draft, 'mode' | 'apiMethod' | 'baseUrl' | 'remoteSiteId' | 'connectionId' | 'property' | 'from' | 'to'>

/** Saves the non-secret choices, so they survive the round trip to Google. */
export function saveDraft(siteId: number, draft: Draft): void {
  try {
    const { mode, apiMethod, baseUrl, remoteSiteId, connectionId, property, from, to } = draft
    const value: Persisted = { mode, apiMethod, baseUrl, remoteSiteId, connectionId, property, from, to }
    window.sessionStorage.setItem(storageKey(siteId), JSON.stringify(value))
  } catch { /* storage can be unavailable; the wizard works without it */ }
}

export function loadDraft(siteId: number): Partial<Persisted> | undefined {
  try {
    const raw = window.sessionStorage.getItem(storageKey(siteId))
    return raw ? (JSON.parse(raw) as Partial<Persisted>) : undefined
  } catch { return undefined }
}

export function clearDraft(siteId: number): void {
  try { window.sessionStorage.removeItem(storageKey(siteId)) } catch { /* ignore */ }
}
