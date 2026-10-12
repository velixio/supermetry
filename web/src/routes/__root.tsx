import { Outlet, createRootRoute } from '@tanstack/react-router'

import { SessionBoundary } from '@/lib/session-lifecycle'
import { ErrorState } from '@/components/ErrorState'

export const Route = createRootRoute({
  component: RootLayout,
  errorComponent: ({ error, reset }) => <ErrorState detail={error instanceof Error ? error.message : String(error)} onRetry={reset} />,
})

function RootLayout() {
  return <SessionBoundary><Outlet /></SessionBoundary>
}
