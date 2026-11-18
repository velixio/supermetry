import { createFileRoute } from '@tanstack/react-router'

import { ReleaseCompare } from '@/features/apps/ReleaseCompare'
import { appSearchInput, parseAppSearch } from '@/features/apps/search'
import { useSite } from '@/features/settings/routeContext'

const version = (value: unknown): string | undefined => (typeof value === 'string' && value.trim() !== '' ? value.trim().slice(0, 64) : undefined)

export const Route = createFileRoute('/app/$org/$site/releases_/compare')({
  validateSearch: (search: Record<string, unknown>) => ({ ...appSearchInput(search), a: version(search.a), b: version(search.b) }),
  component: ReleaseCompareRoute,
})

function ReleaseCompareRoute() {
  const { org, site: slug } = Route.useParams()
  const raw = Route.useSearch()
  const search = parseAppSearch(raw)
  const navigate = Route.useNavigate()
  const { site } = useSite(org, slug)
  if (!site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return (
    <ReleaseCompare
      org={org} site={slug} siteId={site.id} timezone={site.timezone ?? 'UTC'} search={search} a={raw.a ?? ''} b={raw.b ?? ''}
      onSearch={(next) => void navigate({ search: { ...appSearchInput({ ...next }), a: raw.a, b: raw.b } })}
    />
  )
}
