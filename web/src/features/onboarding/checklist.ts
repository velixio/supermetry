export type ChecklistId = 'snippet' | 'firstEvent' | 'goal' | 'invite' | 'mcp'

/** The subset of an API key the checklist needs. The list endpoint has used both casings. */
export type ChecklistKey = {
  scopes?: string[]
  Scopes?: string[]
  revoked?: boolean
  Revoked?: boolean
  last_used_client?: string | null
}

export type ChecklistFacts = {
  /** The site has received at least one accepted event. */
  eventReceived: boolean
  goalCount: number
  memberCount: number
  pendingInvitationCount: number
  keys: ChecklistKey[]
}

export type ChecklistItem = { id: ChecklistId; done: boolean }

/** An AI assistant is connected when a live key can read stats, or an MCP client has already used a key. */
export function mcpConnected(keys: ChecklistKey[]): boolean {
  return keys.some((key) => {
    if (key.last_used_client === 'mcp') return true
    if (key.revoked ?? key.Revoked) return false
    return (key.scopes ?? key.Scopes ?? []).includes('stats:read')
  })
}

export function checklistItems(facts: ChecklistFacts): ChecklistItem[] {
  return [
    { id: 'snippet', done: facts.eventReceived },
    { id: 'firstEvent', done: facts.eventReceived },
    { id: 'goal', done: facts.goalCount > 0 },
    { id: 'invite', done: facts.memberCount > 1 || facts.pendingInvitationCount > 0 },
    { id: 'mcp', done: mcpConnected(facts.keys) },
  ]
}

export function checklistProgress(items: ChecklistItem[]): { done: number; total: number; complete: boolean } {
  const done = items.filter((item) => item.done).length
  return { done, total: items.length, complete: done === items.length }
}

const storagePrefix = 'supermetry.onboarding.dismissed'

function dismissKey(userId: number, siteId: number): string {
  return `${storagePrefix}.${userId}.${siteId}`
}

/** Dismissal is per user per site. Storage can be blocked, so every access is guarded. */
export function readDismissed(userId: number, siteId: number): boolean {
  try {
    return window.localStorage.getItem(dismissKey(userId, siteId)) === '1'
  } catch {
    return false
  }
}

export function writeDismissed(userId: number, siteId: number): void {
  try {
    window.localStorage.setItem(dismissKey(userId, siteId), '1')
  } catch {
    // The checklist reappears on the next visit; nothing else to do.
  }
}
