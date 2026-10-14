import { createFileRoute } from '@tanstack/react-router'

import { CampaignPage } from '@/features/links/CampaignPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/links/pixel_/$campaign')({ component: CampaignRoute })

function CampaignRoute() {
  const { org, site: slug, campaign } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  if (!membership || !site) return null
  return <CampaignPage org={org} segment={campaign} siteId={site.id} siteSlug={slug} timezone={site.timezone || 'UTC'} />
}
