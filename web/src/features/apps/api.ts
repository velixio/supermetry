import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

import { appQuery, type AppSearch } from './search'

export type AppOverviewData = components['schemas']['AppOverview']
export type AppIssueRow = components['schemas']['AppIssueRow']
export type AppIssueList = components['schemas']['AppIssueList']
export type AppIssueDetailData = components['schemas']['AppIssueDetail']
export type AppIssueStatus = components['schemas']['AppIssueStatus']
export type AppCompatibilityData = components['schemas']['AppCompatibility']
export type AppReleasesData = components['schemas']['AppReleases']

const base = (siteId: number) => `/api/private/sites/${siteId}/app`

export function useAppOverview(siteId: number, search: AppSearch, timezone: string, options: { refetchInterval?: number | false } = {}) {
  const query = appQuery(search, timezone).toString()
  return useQuery({ queryKey: ['app-overview', siteId, query], queryFn: () => privateRequest<AppOverviewData>('GET', `${base(siteId)}/overview?${query}`), ...options })
}

export function useAppIssues(siteId: number, search: AppSearch, timezone: string, extra: Record<string, string | number | undefined> = {}) {
  const query = appQuery(search, timezone, extra).toString()
  return useQuery({ queryKey: ['app-issues', siteId, query], queryFn: () => privateRequest<AppIssueList>('GET', `${base(siteId)}/issues?${query}`) })
}

export function useAppIssue(siteId: number, issueId: number, search: AppSearch, timezone: string) {
  const query = appQuery(search, timezone).toString()
  return useQuery({ queryKey: ['app-issue', siteId, issueId, query], queryFn: () => privateRequest<AppIssueDetailData>('GET', `${base(siteId)}/issues/${issueId}?${query}`) })
}

export function useAppCompatibility(siteId: number, search: AppSearch, timezone: string) {
  const query = appQuery(search, timezone).toString()
  return useQuery({ queryKey: ['app-compatibility', siteId, query], queryFn: () => privateRequest<AppCompatibilityData>('GET', `${base(siteId)}/compatibility?${query}`) })
}

export function useAppReleases(siteId: number, search: AppSearch, timezone: string) {
  const query = appQuery(search, timezone).toString()
  return useQuery({ queryKey: ['app-releases', siteId, query], queryFn: () => privateRequest<AppReleasesData>('GET', `${base(siteId)}/releases?${query}`) })
}

export type StatusChange = { status: 'open' | 'resolved' | 'ignored'; resolved_in_release?: string }

/** Optimistic status change for one issue: the cached detail flips at once and is restored if the PATCH fails. */
export function useSetIssueStatus(siteId: number, issueId: number) {
  const client = useQueryClient()
  const detailKey = ['app-issue', siteId, issueId]
  return useMutation({
    mutationFn: (change: StatusChange) => privateRequest<AppIssueStatus>('PATCH', `${base(siteId)}/issues/${issueId}`, { body: change }),
    onMutate: async (change) => {
      await client.cancelQueries({ queryKey: detailKey })
      const previous = client.getQueriesData<AppIssueDetailData>({ queryKey: detailKey })
      client.setQueriesData<AppIssueDetailData>({ queryKey: detailKey }, (data) => data && {
        ...data,
        status: change.status,
        resolved_in_release: change.status === 'resolved' && change.resolved_in_release && change.resolved_in_release !== 'next' ? change.resolved_in_release : null,
        resolved_next: change.status === 'resolved' && change.resolved_in_release === 'next',
      })
      return { previous }
    },
    onError: (_error, _change, context) => {
      for (const [key, data] of context?.previous ?? []) client.setQueryData(key, data)
    },
    onSettled: () => Promise.all([
      client.invalidateQueries({ queryKey: detailKey }),
      client.invalidateQueries({ queryKey: ['app-issues', siteId] }),
      client.invalidateQueries({ queryKey: ['app-overview', siteId] }),
    ]),
  })
}
