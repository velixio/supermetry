import { Outlet, createRootRoute } from '@tanstack/react-router'

import { SessionBoundary } from '@/lib/session-lifecycle'
import { NotFoundPage, RouteErrorPage } from '@/components/NotFound'

export const Route = createRootRoute({
  component: RootLayout,
  errorComponent: ({ error, reset }) => <RouteErrorPage error={error} reset={reset} />,
  notFoundComponent: NotFoundPage,
})

function RootLayout() {
  return <SessionBoundary><Outlet /></SessionBoundary>
}
