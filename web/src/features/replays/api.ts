import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { listReplays, setReplaySaved, type ReplayList, type ReplayListQuery } from '@/lib/replay-api'

export const replayListKeys = {
  all: (siteId: number) => ['replay', siteId, 'list'] as const,
  page: (siteId: number, query: ReplayListQuery) => [...replayListKeys.all(siteId), query] as const,
}

export function useReplays(siteId: number, query: ReplayListQuery) {
  return useQuery({
    queryKey: replayListKeys.page(siteId, query),
    queryFn: ({ signal }) => listReplays(siteId, query, signal),
  })
}

export function useSetReplaySaved(siteId: number) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ sessionId, saved }: { sessionId: string; saved: boolean }) => setReplaySaved(siteId, sessionId, saved),
    onMutate: async ({ sessionId, saved }) => {
      const key = replayListKeys.all(siteId)
      await client.cancelQueries({ queryKey: key })
      const previous = client.getQueriesData<ReplayList>({ queryKey: key })
      for (const [queryKey, page] of previous) {
        if (!page) continue
        const query = queryKey.at(-1) as ReplayListQuery
        const items = page.items.flatMap((item) => {
          if (item.session_id !== sessionId) return [item]
          if ((query.saved === true && !saved) || (query.saved === false && saved)) return []
          return [{ ...item, saved }]
        })
        client.setQueryData<ReplayList>(queryKey, { ...page, items })
      }
      return { previous }
    },
    onError: (_error, _variables, context) => {
      for (const [key, page] of context?.previous ?? []) client.setQueryData(key, page)
    },
    onSettled: () => client.invalidateQueries({ queryKey: replayListKeys.all(siteId) }),
  })
}
