import { privateRequest } from '@/features/settings/privateApi'
import type { ImportPreview, ImportSource } from '@/features/settings/privateApi'

export type ImportStatus = 'pending' | 'running' | 'completed' | 'failed'
export type PullMethod = 'plausible_api' | 'matomo_api' | 'ga4_api'

export type ImportProgress = {
  total_days: number
  done_days: number
  rows: number
  bad_rows: number
  cursor_day: string | null
  updated_at: string
}

export type SiteImport = {
  id: number
  site_id: number
  source: ImportSource | 'matomo'
  status: ImportStatus
  file_name: string
  date_from?: string | null
  date_to?: string | null
  rows: number
  error?: string | null
  created_by?: number | null
  created_at: string
  completed_at?: string | null
  method?: PullMethod
  progress?: ImportProgress | null
}

export type SourceCapability = { available: boolean; reason?: string }
export type Capabilities = Record<PullMethod, SourceCapability>

export type PullBody = {
  method: PullMethod
  from: string
  to: string
  base_url?: string
  remote_site_id?: string
  api_key?: string
  token_auth?: string
  property?: string
  connection_id?: number
}

export type PullPreview = Omit<ImportPreview, 'source'> & { source: ImportSource | 'matomo' }

export type Ga4Connection = { id: number; created_at: string }
export type Ga4Property = { property: string; display_name: string; account: string }

export type SideTotals = { visitors: number; visits: number; pageviews: number }
export type DayComparison = {
  day: string
  imported: SideTotals
  native: SideTotals | null
  visitors_diff_pct: number | null
  pageviews_diff_pct: number | null
}
export type CompareResponse = { from: string; to: string; days: DayComparison[] }

export const importKeys = {
  list: (siteId: number) => ['site-imports', siteId] as const,
  one: (importId: number) => ['site-import', importId] as const,
  capabilities: (siteId: number) => ['import-capabilities', siteId] as const,
  connections: (orgId: number) => ['ga4-connections', orgId] as const,
  properties: (orgId: number, connectionId: number) => ['ga4-properties', orgId, connectionId] as const,
  compare: (siteId: number, from: string, to: string, importId?: number) =>
    ['import-compare', siteId, from, to, importId ?? 0] as const,
}

export function isInFlight(status: string | undefined): boolean {
  return status === 'pending' || status === 'running'
}

/** Poll every 2 s while an import is queued or running, then stop. */
export function pollWhileInFlight(item: { status: string } | undefined): 2_000 | false {
  return item && isInFlight(item.status) ? 2_000 : false
}

const base = (siteId: number) => `/api/private/sites/${siteId}/imports`

export const importsApi = {
  capabilities: (siteId: number, orgId: number) =>
    privateRequest<Capabilities>('GET', `${base(siteId)}/capabilities`, { orgId }),
  list: (siteId: number, orgId: number) => privateRequest<SiteImport[]>('GET', base(siteId), { orgId }),
  get: (importId: number, orgId: number) =>
    privateRequest<SiteImport>('GET', `/api/private/imports/${importId}`, { orgId }),
  remove: (importId: number, orgId: number) =>
    privateRequest<void>('DELETE', `/api/private/imports/${importId}`, { orgId }),
  previewPull: (siteId: number, orgId: number, body: PullBody) =>
    privateRequest<PullPreview>('POST', `${base(siteId)}/pull/preview`, { orgId, body }),
  createPull: (siteId: number, orgId: number, body: PullBody) =>
    privateRequest<SiteImport>('POST', `${base(siteId)}/pull`, { orgId, body }),
  connectGa4: (siteId: number, orgId: number) =>
    privateRequest<{ authorization_url: string }>('POST', `${base(siteId)}/ga4/connect`, { orgId }),
  connections: (orgId: number) =>
    privateRequest<{ data: Ga4Connection[] }>('GET', '/api/private/imports/ga4/connections', { orgId }),
  properties: (orgId: number, connectionId: number) =>
    privateRequest<{ data: Ga4Property[] }>('GET', `/api/private/imports/ga4/connections/${connectionId}/properties`, { orgId }),
  compare: (siteId: number, orgId: number, from: string, to: string, importId?: number) => {
    const query = new URLSearchParams({ from, to })
    if (importId) query.set('import', String(importId))
    return privateRequest<CompareResponse>('GET', `${base(siteId)}/compare?${query}`, { orgId })
  },
}
