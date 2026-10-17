import { describe, expect, it } from 'vitest'

import { lowersRetention, retentionOptions, retentionPolicy, supportHref } from './instanceConfig'

describe('retention policy', () => {
  it('keeps the long-standing defaults until the settings load', () => {
    expect(retentionPolicy(undefined)).toEqual({ defaultDays: 30, forever: true })
  })

  it('uses the configured default and hides forever when it is not offered', () => {
    const policy = retentionPolicy({ default_retention_days: 90, retention_forever: false })
    expect(policy).toEqual({ defaultDays: 90, forever: false })
    expect(retentionOptions(policy)).toEqual([7, 30, 90, 365])
  })

  it('still lists forever for a site that already keeps events forever', () => {
    expect(retentionOptions({ defaultDays: 90, forever: false }, 0)).toEqual([7, 30, 90, 365, 0])
  })

  it('ignores an unsupported or contradictory default', () => {
    expect(retentionPolicy({ default_retention_days: 12, retention_forever: true }).defaultDays).toBe(30)
    expect(retentionPolicy({ default_retention_days: 0, retention_forever: false }).defaultDays).toBe(30)
    expect(retentionPolicy({ default_retention_days: 0, retention_forever: true }).defaultDays).toBe(0)
  })

  it('flags only changes that delete data', () => {
    expect(lowersRetention(365, 7)).toBe(true)
    expect(lowersRetention(0, 90)).toBe(true)
    expect(lowersRetention(30, 90)).toBe(false)
    expect(lowersRetention(30, 0)).toBe(false)
    expect(lowersRetention(30, 30)).toBe(false)
  })
})

describe('supportHref', () => {
  it('allows web and mail links only', () => {
    expect(supportHref('https://help.example/contact')).toBe('https://help.example/contact')
    expect(supportHref('mailto:help@example.com')).toBe('mailto:help@example.com')
    expect(supportHref('javascript:alert(1)')).toBeUndefined()
    expect(supportHref('')).toBeUndefined()
    expect(supportHref('not a url')).toBeUndefined()
  })
})
