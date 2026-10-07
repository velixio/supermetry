import { render } from '@testing-library/react'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from '@tanstack/react-router'
import type { ReactNode } from 'react'

export function renderWithRouter(children: ReactNode, initialEntry = '/') {
  window.scrollTo = () => undefined
  const rootRoute = createRootRoute({ component: () => children })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  return render(<RouterProvider router={router} />)
}
