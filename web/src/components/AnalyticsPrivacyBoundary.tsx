import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { isAnonymousTracking, trackingPolicyKnown } from '@/lib/tracking-policy'
import type { Site } from '@/routes/app.$org'

// These screens issue detailed-history requests that anonymous instances deny.
// Keep this at the outlet: returning a notice from inside a page is too late
// once that page's query hooks have mounted.
const restrictedScreens = new Set([
  'events', 'sessions', 'profiles', 'journeys', 'retention', 'heatmaps',
  'replays', 'funnels', 'vitals', 'goals', 'realtime',
])

export function anonymousRouteRestricted(pathname: string): boolean {
  const [app, , , screen, setting] = pathname.split('/').filter(Boolean).map((segment) => {
    // TanStack matches structural route segments case-insensitively.
    try { return decodeURIComponent(segment).toLowerCase() } catch { return segment.toLowerCase() }
  })
  if (app !== 'app') return false
  return restrictedScreens.has(screen ?? '') ||
    (screen === 'settings' && ['imports', 'goals', 'funnels'].includes(setting ?? ''))
}

export function AnalyticsPrivacyBoundary({
  site, org, requestedSite, pathname, renderedPathnames = [], children,
}: {
  site?: Site
  org: string
  requestedSite?: string
  pathname: string
  renderedPathnames?: readonly string[]
  children: ReactNode
}) {
  const restricted = [pathname, ...renderedPathnames].some(anonymousRouteRestricted)
  if (!restricted || !isAnonymousTracking(site)) {
    return children
  }

  const known = trackingPolicyKnown(site)
  return (
    <section aria-label="Tracking privacy restriction" className="flex max-w-2xl flex-col items-start gap-4 py-6" role="status">
      <h1 className="text-xl font-semibold tracking-tight">
        {known ? 'Unavailable in anonymous tracking mode' : 'Tracking policy is unavailable'}
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        {known
          ? 'This view uses detailed visitor records or historical data that anonymous tracking keeps restricted. Existing data is retained; changing tracking mode does not delete it.'
          : 'Detailed analytics stay restricted until the server provides the instance tracking policy. No detailed visitor data has been requested.'}
      </p>
      <p className="text-sm leading-relaxed text-muted-foreground">
        The dashboard provides supported anonymous analytics. Installation and tracking settings remain available.
      </p>
      {requestedSite ? <Button asChild variant="outline">
        <Link to="/app/$org/$site" params={{ org, site: requestedSite }} search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: false }}>
          Back to dashboard
        </Link>
      </Button> : null}
    </section>
  )
}
