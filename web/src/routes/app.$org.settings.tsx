import { Outlet, createFileRoute, useRouterState } from '@tanstack/react-router'

import { SettingsNav } from '@/features/settings/SettingsNav'
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
    <div className="mx-auto flex w-full min-w-0 max-w-[1200px] flex-col gap-5 lg:flex-row lg:gap-8">
      <aside className="shrink-0 lg:w-52">
        <SettingsNav
          canManage={membership?.role === 'owner' || membership?.role === 'admin'}
          org={org}
          current={current}
          showReplay={membership?.role === 'owner' && replay.enabled}
          importsSite={membership?.sites[0]?.slug}
        />
      </aside>
      <div className="min-w-0 flex-1">
        <Outlet />
      </div>
    </div>
  )
}
