import { useQuery } from '@tanstack/react-query'

import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

import { appQuery, type AppSearch } from '../search'

export type PerformanceOverviewData = components['schemas']['AppPerformanceOverview']
export type TransactionRowData = components['schemas']['AppTransactionRow']
export type TransactionsData = components['schemas']['AppTransactions']
export type TransactionDetailData = components['schemas']['AppTransactionDetail']
export type SpanGroupData = components['schemas']['AppSpanGroup']
export type TraceData = components['schemas']['AppTrace']
export type RegressionsData = components['schemas']['AppRegressions']
export type NetworkFailuresData = components['schemas']['AppNetworkFailures']
export type TransactionSort = 'p95' | 'count' | 'errors' | 'p50'

const base = (siteId: number) => `/api/private/sites/${siteId}/app`

export function usePerformanceOverview(siteId: number, search: AppSearch, timezone: string) {
  const query = appQuery(search, timezone).toString()
  return useQuery({ queryKey: ['app-performance', siteId, query], queryFn: () => privateRequest<PerformanceOverviewData>('GET', `${base(siteId)}/performance?${query}`) })
}

export function useTransactions(siteId: number, search: AppSearch, timezone: string, sort: TransactionSort = 'p95', limit = 50) {
  const query = appQuery(search, timezone, { sort, limit }).toString()
  return useQuery({ queryKey: ['app-transactions', siteId, query], queryFn: () => privateRequest<TransactionsData>('GET', `${base(siteId)}/transactions?${query}`) })
}

export function useTransaction(siteId: number, name: string, search: AppSearch, timezone: string) {
  const query = appQuery(search, timezone, { name }).toString()
  return useQuery({ queryKey: ['app-transaction', siteId, query], queryFn: () => privateRequest<TransactionDetailData>('GET', `${base(siteId)}/transactions/detail?${query}`) })
}

export function useTrace(siteId: number, traceId: string) {
  return useQuery({ queryKey: ['app-trace', siteId, traceId], queryFn: () => privateRequest<TraceData>('GET', `${base(siteId)}/traces/${encodeURIComponent(traceId)}`) })
}

export function useRegressions(siteId: number, search: AppSearch, timezone: string, baseRelease: string, targetRelease: string) {
  const query = appQuery({ ...search, release: undefined }, timezone, { base: baseRelease, target: targetRelease }).toString()
  return useQuery({
    queryKey: ['app-regressions', siteId, query],
    enabled: baseRelease !== '' && targetRelease !== '' && baseRelease !== targetRelease,
    queryFn: () => privateRequest<RegressionsData>('GET', `${base(siteId)}/performance/regressions?${query}`),
  })
}

export function useNetworkFailures(siteId: number, search: AppSearch, timezone: string) {
  const query = appQuery(search, timezone).toString()
  return useQuery({ queryKey: ['app-network', siteId, query], queryFn: () => privateRequest<NetworkFailuresData>('GET', `${base(siteId)}/performance/network?${query}`) })
}
