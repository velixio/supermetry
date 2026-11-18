import { beforeEach, describe, expect, it, vi } from 'vitest'

import { checklistItems, checklistProgress, mcpConnected, readDismissed, writeDismissed, type ChecklistFacts } from './checklist'

const none: ChecklistFacts = { eventReceived: false, goalCount: 0, memberCount: 1, pendingInvitationCount: 0, keys: [] }

describe('checklist completion', () => {
  it('starts with nothing done', () => {
    expect(checklistProgress(checklistItems(none))).toEqual({ done: 0, total: 5, complete: false })
  })

  it('marks the snippet and first event once an event arrived', () => {
    const items = checklistItems({ ...none, eventReceived: true })
    expect(items.filter((item) => item.done).map((item) => item.id)).toEqual(['snippet', 'firstEvent'])
  })

  it('counts a goal, a second member or a pending invitation', () => {
    expect(checklistItems({ ...none, goalCount: 1 }).find((item) => item.id === 'goal')?.done).toBe(true)
    expect(checklistItems({ ...none, memberCount: 2 }).find((item) => item.id === 'invite')?.done).toBe(true)
    expect(checklistItems({ ...none, pendingInvitationCount: 1 }).find((item) => item.id === 'invite')?.done).toBe(true)
  })

  it('completes when all five are done', () => {
    const items = checklistItems({ eventReceived: true, goalCount: 2, memberCount: 3, pendingInvitationCount: 0, keys: [{ scopes: ['stats:read'] }] })
    expect(checklistProgress(items)).toEqual({ done: 5, total: 5, complete: true })
  })
})

describe('mcpConnected', () => {
  it('needs a live key with stats:read', () => {
    expect(mcpConnected([{ scopes: ['stats:read'] }])).toBe(true)
    expect(mcpConnected([{ Scopes: ['stats:read'] }])).toBe(true)
    expect(mcpConnected([{ scopes: ['sites:write'] }])).toBe(false)
    expect(mcpConnected([{ scopes: ['stats:read'], revoked: true }])).toBe(false)
    expect(mcpConnected([])).toBe(false)
  })

  it('counts a key an MCP client has used, even if revoked since', () => {
    expect(mcpConnected([{ scopes: [], revoked: true, last_used_client: 'mcp' }])).toBe(true)
    expect(mcpConnected([{ scopes: [], last_used_client: 'curl' }])).toBe(false)
  })
})

describe('dismissal persistence', () => {
  beforeEach(() => window.localStorage.clear())

  it('is remembered per user and site', () => {
    expect(readDismissed(1, 10)).toBe(false)
    writeDismissed(1, 10)
    expect(readDismissed(1, 10)).toBe(true)
    expect(readDismissed(2, 10)).toBe(false)
    expect(readDismissed(1, 11)).toBe(false)
  })

  it('survives blocked storage', () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    expect(readDismissed(1, 10)).toBe(false)
    expect(() => writeDismissed(1, 10)).not.toThrow()
    spy.mockRestore()
    set.mockRestore()
  })
})
