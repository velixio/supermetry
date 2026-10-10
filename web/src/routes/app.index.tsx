import { useCallback, useEffect } from 'react'
import { createFileRoute, useRouter } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { DemoChoice } from '@/features/demo/DemoChoice'
import { createSite as createManagedSite } from '@/features/sites/api'
import { SiteForm } from '@/features/sites/SiteForm'
import type { ManagedSite, SiteInput } from '@/features/sites/types'
import { en } from '@/messages/en'
import { canCreateSites } from '@/components/Sidebar'
import { AccountFrame } from '@/components/AccountFrame'
import { NoOrgScreen } from '@/features/lifecycle/NoOrgScreen'
import { useMe, type Me } from './app.$org'

export const Route = createFileRoute('/app/')({
  component: IndexPage,
})

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
  if (!membership) {
    return (
      <AccountFrame me={me.data}>
        <NoOrgScreen navigate={navigate} />
      </AccountFrame>
    )
  }
  if (membership && (membership.sites.length > 0 || !canCreateSites(membership.role))) {
    return <Onboarding me={me.data} navigate={navigate} />
  }
  return (
    <AccountFrame me={me.data}><Onboarding
      me={me.data}
      navigate={navigate}
    /></AccountFrame>
  )
}

function siteSlug(site: Me['memberships'][number]['sites'][number]): string {
  return site.slug ?? site.public_id ?? String(site.id)
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

export function Onboarding({
  me,
  createSite,
  navigate,
}: {
  me: Me
  createSite?: (input: SiteInput) => Promise<ManagedSite>
  navigate: (path: string, replace?: boolean) => void
}) {
  const membership = me.memberships[0]
  const existingSite = membership?.sites[0]

  useEffect(() => {
    if (!membership) return
    if (existingSite) navigate(`/app/${membership.org.slug}/${siteSlug(existingSite)}`, true)
    else if (!canCreateSites(membership.role)) navigate(`/app/${membership.org.slug}/settings`, true)
  }, [existingSite, membership, navigate])

  if (!membership) {
    return <section className="mx-auto max-w-lg py-12"><h1 className="text-2xl font-semibold">No organization access</h1><p className="mt-2 text-muted-foreground">Ask an organization owner for an invitation. Your account settings are available from the Account menu.</p></section>
  }

  if (existingSite || !canCreateSites(membership.role)) {
    return (
      <main className="grid min-h-dvh place-items-center px-6">
        <p className="text-sm text-muted-foreground">{en.loadingDashboard}</p>
      </main>
    )
  }

  return (
    <main className="grid min-h-dvh place-items-center px-6 py-10">
      <section className="flex w-full max-w-md flex-col gap-5 border-l-2 border-accent pl-5">
        {me.demo_prompt_dismissed === false ? (
          <DemoChoice
            org={{ id: membership.org.id, slug: membership.org.slug }}
            onDismissed={() => navigate(`/app/${membership.org.slug}/sites`)}
          />
        ) : null}
        <div className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Create your first site</h1>
          <p className="text-sm text-muted-foreground">Add the website where Supermetry should collect analytics.</p>
        </div>
        <SiteForm
          submitLabel="Create site"
          onSubmit={async (input) => {
            const site = await (createSite ?? ((value) => createManagedSite(value, membership.org.id)))(input)
            navigate(`/app/${membership.org.slug}/${site.public_id}/settings/installation`)
          }}
          secondaryAction={me.demo_prompt_dismissed !== false ? (
            <Button type="button" variant="ghost" onClick={() => navigate(`/app/${membership.org.slug}/sites`)}>
              Skip for now
            </Button>
          ) : undefined}
        />
      </section>
    </main>
  )
}
