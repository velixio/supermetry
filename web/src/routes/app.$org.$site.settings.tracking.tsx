import { createFileRoute } from '@tanstack/react-router'

import { DataPurgeCard } from '@/features/sites/DataPurgeCard'
import { ExclusionsPage } from '@/features/settings/ExclusionsPage'
import { TrackingSettings } from '@/features/settings/TrackingSettings'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/settings/tracking')({ component: ExclusionsRoute })

function ExclusionsRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  return membership && site ? (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
      <TrackingSettings
        orgId={membership.org.id}
        role={membership.role}
        siteId={site.id}
      />
      <ExclusionsPage
        orgId={membership.org.id}
        role={membership.role}
        siteId={site.id}
        canManageHeatmaps={membership.role === 'owner' || membership.role === 'admin'}
      />
      {membership.role === 'owner' || membership.role === 'admin' ? (
        <DataPurgeCard orgId={membership.org.id} siteId={site.id} />
      ) : null}
    </div>
  ) : null
}
