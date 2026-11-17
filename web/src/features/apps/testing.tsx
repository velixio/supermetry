import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import type { ReactNode } from 'react'

import type { AppIssueRow } from './api'

export function renderWithQuery(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}>{children}</QueryClientProvider>)
}

export const issueRow = (over: Partial<AppIssueRow> = {}): AppIssueRow => ({
  id: 11, title: 'Segfault in renderer', kind: 'crash', error_type: 'SIGSEGV', status: 'open',
  first_seen: '2026-11-01T10:00:00Z', last_seen: '2026-11-14T10:00:00Z', first_release: '1.0.0', last_release: '1.2.0',
  events: 42, users: 9, sparkline: [1, 2, 0, 5], assignee_user_id: null, assignee_name: '', priority: 'medium', ...over,
})
