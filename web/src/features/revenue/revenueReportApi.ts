import { useQuery } from '@tanstack/react-query'

import type { components } from '@/lib/api.gen'

export type RevenueReportData = components['schemas']['RevenueReport']
export type RevenueReportRow = components['schemas']['RevenueReportRow']
export type RevenueCustomer = components['schemas']['RevenueCustomer']
export type RevenueBreakdownKey = 'country' | 'region' | 'source' | 'campaign' | 'page' | 'product'
export type RevenueAttribution = 'event' | 'first_click' | 'last_click'

export const breakdownKeys: readonly RevenueBreakdownKey[] = ['country', 'region', 'source', 'campaign', 'page', 'product']
export const attributionKeys: readonly RevenueAttribution[] = ['event', 'first_click', 'last_click']
export const lookbackDefault = 30

export class RevenueRequestError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'RevenueRequestError'
    this.status = status
  }
}

export type RevenueReportParams = {
  from: string
  to: string
  breakdown: RevenueBreakdownKey
  attribution: RevenueAttribution
  lookbackDays: number
  limit?: number
}

export const revenueReportKeys = {
  site: (siteId: number) => ['sites', siteId, 'revenue-report'] as const,
  report: (siteId: number, params: RevenueReportParams) => [...revenueReportKeys.site(siteId), 'report', params] as const,
  customers: (siteId: number, from: string, to: string, search: string) => [...revenueReportKeys.site(siteId), 'customers', from, to, search] as const,
}

export function clampLookback(value: number): number {
  if (!Number.isFinite(value)) return lookbackDefault
  return Math.min(90, Math.max(1, Math.trunc(value)))
}

export function reportPath(siteId: number, params: RevenueReportParams): string {
  const search = new URLSearchParams({
    from: params.from,
    to: params.to,
    breakdown: params.breakdown,
    attribution: params.attribution,
    lookback_days: String(clampLookback(params.lookbackDays)),
  })
  if (params.limit) search.set('limit', String(params.limit))
  return `/api/private/sites/${siteId}/revenue/report?${search}`
}

async function revenueFetch(path: string, orgId: number, accept: string): Promise<Response> {
  const response = await fetch(path, {
    headers: { Accept: accept, 'X-Supermetry-Org': String(orgId) },
    credentials: 'same-origin',
  })
  if (!response.ok) {
    let detail = ''
    try {
      const problem = (await response.json()) as { detail?: string }
      detail = problem.detail ?? ''
    } catch {
      // The body is not a problem document.
    }
    throw new RevenueRequestError(response.status, detail || `Request failed (${response.status})`)
  }
  return response
}

export function useRevenueBreakdown(siteId: number, orgId: number, params: RevenueReportParams) {
  return useQuery({
    queryKey: revenueReportKeys.report(siteId, params),
    queryFn: async () => (await revenueFetch(reportPath(siteId, params), orgId, 'application/json')).json() as Promise<RevenueReportData>,
    retry: false,
  })
}

export function useRevenueCustomers(siteId: number, orgId: number, from: string, to: string, search: string, enabled: boolean) {
  const query = new URLSearchParams({ from, to, limit: '50' })
  if (search) query.set('search', search)
  return useQuery({
    enabled,
    queryKey: revenueReportKeys.customers(siteId, from, to, search),
    queryFn: async () => ((await (await revenueFetch(`/api/private/sites/${siteId}/revenue/customers?${query}`, orgId, 'application/json')).json()) as components['schemas']['RevenueCustomersResponse']).data,
    retry: false,
  })
}

export async function downloadRevenueCsv(siteId: number, orgId: number, params: RevenueReportParams): Promise<void> {
  const response = await revenueFetch(reportPath(siteId, { ...params, limit: 500 }), orgId, 'text/csv')
  const url = URL.createObjectURL(await response.blob())
  const link = document.createElement('a')
  link.href = url
  link.download = `revenue-${params.breakdown}-${params.from}-${params.to}.csv`
  link.click()
  URL.revokeObjectURL(url)
}
