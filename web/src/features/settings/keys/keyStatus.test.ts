import { describe, expect, it } from 'vitest'

import { isUnused, keyStatus, statusVariant, type ApiKeyItem } from './keyStatus'

const now = new Date('2026-11-18T12:00:00Z').getTime()
const base: ApiKeyItem = {
  id: 1, name: 'k', prefix: 'pnl_x', scopes: ['stats:read'], site_ids: null,
  created_at: '2026-11-01T00:00:00Z', expires_at: '2027-02-01T00:00:00Z', last_used_at: '2026-11-17T00:00:00Z', revoked: false,
}

describe('key status chips', () => {
  it.each([
    ['active', {}, 'active', 'success'],
    ['expiring soon (server flag)', { expiring_soon: true }, 'expiring', 'warning'],
    ['no expiry', { expires_at: null, never_expires: true }, 'noExpiry', 'warning-outline'],
    ['expired', { expires_at: '2026-11-10T00:00:00Z' }, 'expired', 'neutral'],
    ['revoked wins over everything', { revoked: true, expires_at: null }, 'revoked', 'neutral'],
  ] as const)('maps %s', (_label, patch, status, variant) => {
    const key = { ...base, ...patch } as ApiKeyItem
    expect(keyStatus(key, now)).toBe(status)
    expect(statusVariant[keyStatus(key, now)]).toBe(variant)
  })

  it('flags live keys unused for 90 days, never revoked or expired ones', () => {
    expect(isUnused({ ...base, last_used_at: '2026-08-01T00:00:00Z' }, now)).toBe(true)
    expect(isUnused({ ...base, last_used_at: null, created_at: '2026-08-01T00:00:00Z' }, now)).toBe(true)
    expect(isUnused({ ...base, last_used_at: '2026-11-01T00:00:00Z' }, now)).toBe(false)
    expect(isUnused({ ...base, last_used_at: '2026-01-01T00:00:00Z', revoked: true }, now)).toBe(false)
  })
})
