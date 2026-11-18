export type Purpose = 'stats' | 'mcp' | 'ci'

export const apiKeyScopes = [
  'stats:read',
  'profiles:read',
  'replay:read',
  'data:export',
  'sites:write',
  'goals:write',
  'segments:write',
  'annotations:write',
  'keys:manage',
] as const

export type ExpiryChoice = '30' | '90' | '180' | '365' | 'custom' | 'never'

/** What each purpose pre-selects. Everything stays editable on the next step. */
export const purposePresets: Record<Purpose, { scopes: string[] }> = {
  stats: { scopes: ['stats:read'] },
  mcp: { scopes: ['stats:read'] },
  ci: { scopes: ['stats:read', 'data:export'] },
}

export const DEFAULT_EXPIRY: ExpiryChoice = '90'

export type CreateKeyBody = {
  name: string
  scopes: string[]
  site_ids: number[] | null
  expires_in_days?: number
  expires_at?: string
  never_expires?: true
}

export function expiryFields(choice: ExpiryChoice, customDate: string): Pick<CreateKeyBody, 'expires_in_days' | 'expires_at' | 'never_expires'> {
  if (choice === 'never') return { never_expires: true }
  if (choice === 'custom') return { expires_at: new Date(`${customDate}T00:00:00.000Z`).toISOString() }
  return { expires_in_days: Number(choice) }
}

export function mcpEndpoint(origin = window.location.origin): string {
  return `${origin}/api/public/v1/mcp`
}

export function mcpConfig(key: string, origin?: string): string {
  return JSON.stringify(
    { mcpServers: { supermetry: { type: 'http', url: mcpEndpoint(origin), headers: { Authorization: `Bearer ${key}` } } } },
    null,
    2,
  )
}
