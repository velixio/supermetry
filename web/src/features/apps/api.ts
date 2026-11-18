import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

import { appQuery, type AppSearch } from './search'

export type AppOverviewData = components['schemas']['AppOverview']
export type AppIssueRow = components['schemas']['AppIssueRow']
export type AppIssueList = components['schemas']['AppIssueList']
export type AppIssueDetailData = components['schemas']['AppIssueDetail']
export type AppIssueStatus = components['schemas']['AppIssueStatus']
export type AppIssueChange = components['schemas']['AppIssueChange']
export type AppIssueComment = components['schemas']['AppIssueComment']
export type AppIssueBulkResult = components['schemas']['AppIssueBulkResult']
export type AppSavedView = components['schemas']['AppSavedView']
export type AppIssueActivity = components['schemas']['AppIssueActivity']
export type AppMergedIssue = components['schemas']['AppMergedIssue']
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
      client.invalidateQueries({ queryKey: ['app-issue-activity', siteId, issueId] }),
    ]),
  })
}

/** Assignee, priority, snooze, archive and GitHub link changes; every one adds a timeline row, so the activity refetches. */
export function useUpdateIssue(siteId: number, issueId: number) {
  const client = useQueryClient()
  const detailKey = ['app-issue', siteId, issueId]
  return useMutation({
    mutationFn: (change: AppIssueChange) => privateRequest<AppIssueStatus>('PATCH', `${base(siteId)}/issues/${issueId}`, { body: change }),
    onMutate: async (change) => {
      await client.cancelQueries({ queryKey: detailKey })
      const previous = client.getQueriesData<AppIssueDetailData>({ queryKey: detailKey })
      if (change.priority) client.setQueriesData<AppIssueDetailData>({ queryKey: detailKey }, (data) => data && { ...data, priority: change.priority! })
      return { previous }
    },
    onError: (_error, _change, context) => {
      for (const [key, data] of context?.previous ?? []) client.setQueryData(key, data)
    },
    onSettled: () => Promise.all([
      client.invalidateQueries({ queryKey: detailKey }),
      client.invalidateQueries({ queryKey: ['app-issues', siteId] }),
      client.invalidateQueries({ queryKey: ['app-issue-activity', siteId, issueId] }),
    ]),
  })
}

export function useAppAssignees(siteId: number) {
  return useQuery({ queryKey: ['app-assignees', siteId], queryFn: () => privateRequest<{ members: { user_id: number; name: string }[] }>('GET', `${base(siteId)}/assignees`), staleTime: 60_000 })
}

export function useIssueComments(siteId: number, issueId: number) {
  return useQuery({ queryKey: ['app-issue-comments', siteId, issueId], queryFn: () => privateRequest<{ comments: AppIssueComment[] }>('GET', `${base(siteId)}/issues/${issueId}/comments`) })
}

export function useCommentMutations(siteId: number, issueId: number) {
  const client = useQueryClient()
  const refresh = () => Promise.all([
    client.invalidateQueries({ queryKey: ['app-issue-comments', siteId, issueId] }),
    client.invalidateQueries({ queryKey: ['app-issue-activity', siteId, issueId] }),
  ])
  const path = `${base(siteId)}/issues/${issueId}/comments`
  return {
    post: useMutation({ mutationFn: (body: string) => privateRequest<AppIssueComment>('POST', path, { body: { body } }), onSuccess: refresh }),
    remove: useMutation({ mutationFn: (id: number) => privateRequest<void>('DELETE', `${path}/${id}`), onSuccess: refresh }),
  }
}

/** What a bulk action applies to: the checked rows, or everything a search matches (range and filters ride in the URL). */
export type BulkTarget = { ids: number[] } | { query: string; params: string }

export function useBulkIssues(siteId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ target, change }: { target: BulkTarget; change: AppIssueChange }) => {
      if ('ids' in target) return privateRequest<AppIssueBulkResult>('POST', `${base(siteId)}/issues/bulk`, { body: { ids: target.ids, change } })
      return privateRequest<AppIssueBulkResult>('POST', `${base(siteId)}/issues/bulk?${target.params}`, { body: { query: target.query, change } })
    },
    onSettled: () => Promise.all([
      client.invalidateQueries({ queryKey: ['app-issues', siteId] }),
      client.invalidateQueries({ queryKey: ['app-overview', siteId] }),
      client.invalidateQueries({ queryKey: ['app-issue', siteId] }),
    ]),
  })
}

export function useSavedViews(siteId: number) {
  return useQuery({ queryKey: ['app-views', siteId], queryFn: () => privateRequest<{ views: AppSavedView[] }>('GET', `${base(siteId)}/views`) })
}

export function useSavedViewMutations(siteId: number) {
  const client = useQueryClient()
  const refresh = () => client.invalidateQueries({ queryKey: ['app-views', siteId] })
  return {
    create: useMutation({ mutationFn: (view: { name: string; query: string; shared: boolean }) => privateRequest<AppSavedView>('POST', `${base(siteId)}/views`, { body: view }), onSuccess: refresh }),
    remove: useMutation({ mutationFn: (id: number) => privateRequest<void>('DELETE', `${base(siteId)}/views/${id}`), onSuccess: refresh }),
  }
}

export const ACTIVITY_PAGE = 50

/** The timeline, newest first; older pages continue from the last row's time. */
export function useIssueActivity(siteId: number, issueId: number) {
  return useInfiniteQuery({
    queryKey: ['app-issue-activity', siteId, issueId],
    initialPageParam: '',
    queryFn: ({ pageParam }) => privateRequest<{ activity: AppIssueActivity[] }>('GET', `${base(siteId)}/issues/${issueId}/activity?limit=${ACTIVITY_PAGE}${pageParam ? `&before=${encodeURIComponent(pageParam)}` : ''}`),
    getNextPageParam: (last) => (last.activity.length === ACTIVITY_PAGE ? last.activity[last.activity.length - 1].at : undefined),
  })
}

export function useMergedIssues(siteId: number, issueId: number) {
  return useQuery({ queryKey: ['app-issue-merged', siteId, issueId], queryFn: () => privateRequest<{ merged: AppMergedIssue[] }>('GET', `${base(siteId)}/issues/${issueId}/merged`) })
}

/** Merge other issues into this one, or split merged ones back out by fingerprint hash. */
export function useMergeMutations(siteId: number, issueId: number) {
  const client = useQueryClient()
  const refresh = () => Promise.all([
    client.invalidateQueries({ queryKey: ['app-issue', siteId] }),
    client.invalidateQueries({ queryKey: ['app-issues', siteId] }),
    client.invalidateQueries({ queryKey: ['app-overview', siteId] }),
    client.invalidateQueries({ queryKey: ['app-issue-merged', siteId, issueId] }),
    client.invalidateQueries({ queryKey: ['app-issue-activity', siteId, issueId] }),
  ])
  const path = `${base(siteId)}/issues/${issueId}`
  return {
    merge: useMutation({ mutationFn: (sourceIds: number[]) => privateRequest<{ merged: number[] }>('POST', `${path}/merge`, { body: { source_ids: sourceIds } }), onSuccess: refresh }),
    unmerge: useMutation({ mutationFn: (hash: string) => privateRequest<{ issue_id: number }>('POST', `${path}/unmerge`, { body: { hashes: [hash] } }), onSuccess: refresh }),
  }
}
