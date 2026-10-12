import { lazy, Suspense, useCallback } from 'react'
import { createFileRoute, useRouter } from '@tanstack/react-router'

import { en } from '@/messages/en'
import { NoOrgScreen } from '@/features/lifecycle/NoOrgScreen'
import { parseSitesOverviewSearch } from '@/features/sites/overviewSearch'
import { useMe } from './app.$org'

export const Route = createFileRoute('/app/')({
  validateSearch: parseSitesOverviewSearch,
  component: IndexPage,
})

const SitesOverviewLanding = lazy(() => import('@/features/sites/SitesOverviewLanding').then((module) => ({ default: module.SitesOverviewLanding })))
const Onboarding = lazy(() => import('@/features/sites/Onboarding').then((module) => ({ default: module.Onboarding })))

export function IndexPage() {
  const me = useMe()
  const router = useRouter()
  const navigate = useCallback(
    (path: string, replace?: boolean) => {
      if (replace) router.history.replace(path)
      else router.history.push(path)
    },
    [router],
  )

  if (me.isError) {
    return <OnboardingError detail="Your account could not be loaded." />
  }
  if (!me.data) {
    return (
      <main className="grid min-h-dvh place-items-center px-6">
        <p className="text-sm text-muted-foreground">{en.loadingDashboard}</p>
      </main>
    )
  }

  const membership = me.data.memberships[0]
  const allSites = me.data.memberships.flatMap((item) => item.sites.map((site) => ({ membership: item, site })))
  if (!membership) return <NoOrgScreen navigate={navigate} />
  if (allSites.length > 0) {
    return (
      <Suspense fallback={<div className="h-72 animate-pulse rounded-xl bg-muted" aria-label="Loading sites" role="status" />}><SitesOverviewLanding me={me.data} /></Suspense>
    )
  }
  return (
    <Suspense fallback={<p className="text-sm text-muted-foreground" role="status">{en.loadingDashboard}</p>}><Onboarding me={me.data} navigate={navigate} /></Suspense>
  )
}
function OnboardingError({ detail }: { detail: string }) {
  return (
    <main className="grid min-h-dvh place-items-center px-6">
      <div className="flex max-w-sm flex-col gap-2 border-l-2 border-destructive pl-5">
        <h1 className="text-2xl font-semibold tracking-tight">{en.appName}</h1>
        <p role="alert" className="text-sm text-muted-foreground">{detail}</p>
      </div>
    </main>
  )
}
