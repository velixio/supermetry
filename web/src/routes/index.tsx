import { useCallback, useEffect } from 'react'
import { createFileRoute, useRouter } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { createSite as createManagedSite } from '@/features/sites/api'
import { SiteForm } from '@/features/sites/SiteForm'
import type { ManagedSite, SiteInput } from '@/features/sites/types'
import { en } from '@/messages/en'
import { canCreateSites } from '@/components/Sidebar'
import { useMe, type Me } from './$org'

export const Route = createFileRoute('/')({
  component: IndexPage,
})

function IndexPage() {
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

  return (
    <Onboarding
      me={me.data}
      navigate={navigate}
    />
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
    if (existingSite) navigate(`/${membership.org.slug}/${siteSlug(existingSite)}`, true)
    else if (!canCreateSites(membership.role)) navigate(`/${membership.org.slug}/settings`, true)
  }, [existingSite, membership, navigate])

  if (!membership) {
    return <OnboardingError detail="Your account does not belong to an organization." />
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
        <div className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Create your first site</h1>
          <p className="text-sm text-muted-foreground">Add the website where Panel should collect analytics.</p>
        </div>
        <SiteForm
          submitLabel="Create site"
          onSubmit={async (input) => {
            const site = await (createSite ?? ((value) => createManagedSite(value, membership.org.id)))(input)
            navigate(`/${membership.org.slug}/${site.public_id}/settings/installation`)
          }}
          secondaryAction={
            <Button type="button" variant="ghost" onClick={() => navigate(`/${membership.org.slug}/sites`)}>
              Skip for now
            </Button>
          }
        />
      </section>
    </main>
  )
}
