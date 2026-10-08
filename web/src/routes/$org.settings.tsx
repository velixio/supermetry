import { Outlet, createFileRoute, useRouterState } from '@tanstack/react-router'

import { OrganizationSettingsTabs } from '@/features/settings/OrganizationSettings'

export const Route = createFileRoute('/$org/settings')({ component: OrganizationSettingsLayout })

function OrganizationSettingsLayout() {
  const { org } = Route.useParams()
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const current = pathname.split('/').filter(Boolean).at(-1) ?? 'members'
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <OrganizationSettingsTabs org={org} current={current} />
      <Outlet />
    </div>
  )
}
