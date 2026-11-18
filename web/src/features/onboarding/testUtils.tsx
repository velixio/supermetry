import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from '@tanstack/react-router'
import { render } from '@testing-library/react'
import type { ReactNode } from 'react'

/** Renders UI inside a memory router and a fresh query client, for tests of links and queries. */
export async function renderWithRouter(ui: ReactNode, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  const root = createRootRoute({ component: () => <>{ui}</> })
  const router = createRouter({ routeTree: root, history: createMemoryHistory({ initialEntries: ['/'] }) })
  await router.load()
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}
