import { isAnonymousTracking } from '@/lib/tracking-policy'
import { Outlet, createFileRoute, useRouterState } from '@tanstack/react-router'

import { SiteSettingsTabs } from '@/features/settings/SiteSettings'
import { useSite } from '@/features/settings/routeContext'
import { useReplayAvailability } from '@/components/replay/api'

export const Route = createFileRoute('/$org/$site/settings')({ component: SiteSettingsLayout })

function SiteSettingsLayout() {
  const { org, site } = Route.useParams()
  const siteContext = useSite(org, site)
  const replay = useReplayAvailability(siteContext.site?.id)
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const current = pathname.split('/').filter(Boolean).at(-1) ?? 'general'
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <SiteSettingsTabs org={org} site={site} current={current} showReplay={replay.isSuccess} trackingRestricted={isAnonymousTracking(siteContext.site)} />
      <Outlet />
    </div>
  )
}
