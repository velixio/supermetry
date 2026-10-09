import { Outlet, createFileRoute, useRouterState } from '@tanstack/react-router'

import { OrganizationSettingsTabs } from '@/features/settings/OrganizationSettings'
import { useReplayAvailability } from '@/components/replay/api'

import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/settings')({ component: OrganizationSettingsLayout })

function OrganizationSettingsLayout() {
  const { org } = Route.useParams()
  const membership = useMe().data?.memberships.find((item) => item.org.slug === org)
  const replay = useReplayAvailability(membership?.role === 'owner' ? membership.sites[0]?.id : undefined)
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const current = pathname.split('/').filter(Boolean).at(-1) ?? 'members'
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <OrganizationSettingsTabs canManage={membership?.role === 'owner' || membership?.role === 'admin'} org={org} current={current} showReplay={membership?.role === 'owner' && replay.enabled} />
      <Outlet />
    </div>
  )
}
