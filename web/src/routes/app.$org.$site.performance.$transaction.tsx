import { createFileRoute } from '@tanstack/react-router'

import { appSearchInput, parseAppSearch } from '@/features/apps/search'
import { TransactionDetail } from '@/features/apps/performance/TransactionDetail'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/performance/$transaction')({
  validateSearch: appSearchInput,
  component: TransactionRoute,
})

function TransactionRoute() {
  const { org, site: slug, transaction } = Route.useParams()
  const search = parseAppSearch(Route.useSearch())
  const navigate = Route.useNavigate()
  const { site } = useSite(org, slug)
  if (!site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return (
    <TransactionDetail
      org={org} site={slug} siteId={site.id} name={transaction} timezone={site.timezone ?? 'UTC'} search={search}
      onSearch={(next) => void navigate({ search: appSearchInput({ ...next }) })}
    />
  )
}
