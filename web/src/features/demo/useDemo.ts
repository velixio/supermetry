import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'

import { createDemo, dismissDemoPrompt } from './api'

export type DemoOrganization = { id: number; slug: string }

function browserTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
}

export function useCreateDemo(org: DemoOrganization) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => createDemo(org.id, browserTimezone()),
    onSuccess: async (site) => {
      void queryClient.invalidateQueries({ queryKey: ['me'] })
      await navigate({
        to: '/app/$org/$site',
        params: { org: org.slug, site: site.public_id },
        search: { period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true },
      })
    },
  })
}

export function useDismissDemoPrompt() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: dismissDemoPrompt,
    onSuccess: () => {
      queryClient.setQueriesData<{ demo_prompt_dismissed?: boolean }>(
        { queryKey: ['me'] },
        (me) => me ? { ...me, demo_prompt_dismissed: true } : me,
      )
    },
  })
}
