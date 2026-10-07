import { createFileRoute } from '@tanstack/react-router'

import { SharedDashboard } from '@/features/share/SharedDashboard'

export const Route = createFileRoute('/share/$slug')({
  component: ShareRoute,
})

function ShareRoute() {
  const { slug } = Route.useParams()
  return <SharedDashboard slug={slug} />
}
