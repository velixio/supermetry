import { Outlet, createRootRoute } from '@tanstack/react-router'

import { SessionBoundary } from '@/lib/session-lifecycle'

export const Route = createRootRoute({
  component: RootLayout,
})

function RootLayout() {
  return <SessionBoundary><Outlet /></SessionBoundary>
}
