import type { StatusChipVariant } from '@/components/StatusChip'

export type ApiKeyItem = {
  id: number
  name: string
  prefix: string
  scopes: string[]
  site_ids: number[] | null
  created_at: string
  expires_at: string | null
  last_used_at: string | null
  last_used_country?: string | null
  last_used_client?: 'mcp' | 'curl' | 'sdk-js' | 'sdk-python' | 'browser' | 'other' | null
  revoked: boolean
  never_expires?: boolean
  expiring_soon?: boolean
}

export type KeyStatus = 'active' | 'expiring' | 'noExpiry' | 'expired' | 'revoked'

const DAY = 86_400_000
export const UNUSED_AFTER_DAYS = 90
const EXPIRING_SOON_DAYS = 14

export function keyStatus(key: ApiKeyItem, now = Date.now()): KeyStatus {
  if (key.revoked) return 'revoked'
  if (key.expires_at && new Date(key.expires_at).getTime() <= now) return 'expired'
  if (key.never_expires || !key.expires_at) return 'noExpiry'
  if (key.expiring_soon ?? new Date(key.expires_at).getTime() - now <= EXPIRING_SOON_DAYS * DAY) return 'expiring'
  return 'active'
}

export const statusVariant: Record<KeyStatus, StatusChipVariant> = {
  active: 'success',
  expiring: 'warning',
  noExpiry: 'warning-outline',
  expired: 'neutral',
  revoked: 'neutral',
}

/** A live key that has not been used for 90 days (or never, and is older than that). */
export function isUnused(key: ApiKeyItem, now = Date.now()): boolean {
  const status = keyStatus(key, now)
  if (status === 'revoked' || status === 'expired') return false
  const since = key.last_used_at ?? key.created_at
  return now - new Date(since).getTime() >= UNUSED_AFTER_DAYS * DAY
}

export function relativeTime(value: string, locale: string, now = Date.now()): string {
  const formatter = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  const seconds = Math.round((new Date(value).getTime() - now) / 1000)
  if (Math.abs(seconds) < 60) return formatter.format(seconds, 'second')
  const minutes = Math.round(seconds / 60)
  if (Math.abs(minutes) < 60) return formatter.format(minutes, 'minute')
  const hours = Math.round(minutes / 60)
  if (Math.abs(hours) < 24) return formatter.format(hours, 'hour')
  return formatter.format(Math.round(hours / 24), 'day')
}
