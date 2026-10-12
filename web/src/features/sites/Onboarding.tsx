import { useEffect } from 'react'

import { Button } from '@/components/ui/button'
import { canCreateSites } from '@/components/Sidebar'
import { DemoChoice } from '@/features/demo/DemoChoice'
import { createSite as createManagedSite } from '@/features/sites/api'
import { SiteForm } from '@/features/sites/SiteForm'
import type { ManagedSite, SiteInput } from '@/features/sites/types'
import { en } from '@/messages/en'
import type { Me } from '@/routes/app.$org'

export function Onboarding({ me, createSite, navigate }: {
  me: Me
  createSite?: (input: SiteInput) => Promise<ManagedSite>
  navigate: (path: string, replace?: boolean) => void
}) {
  const membership = me.memberships[0]
  const existingSite = membership?.sites[0]

  useEffect(() => {
    if (!membership) return
    if (existingSite) navigate(`/app/${membership.org.slug}/${existingSite.slug ?? existingSite.public_id ?? String(existingSite.id)}`, true)
    else if (!canCreateSites(membership.role)) navigate(`/app/${membership.org.slug}/settings`, true)
  }, [existingSite, membership, navigate])

  if (!membership) return <section className="mx-auto max-w-lg py-12"><h1 className="text-2xl font-semibold">No organization access</h1><p className="mt-2 text-muted-foreground">Ask an organization owner for an invitation. Your account settings are available from the Account menu.</p></section>

  if (existingSite || !canCreateSites(membership.role)) return <main className="grid min-h-dvh place-items-center px-6"><p className="text-sm text-muted-foreground">{en.loadingDashboard}</p></main>

  return (
    <main className="grid min-h-[calc(100dvh-3.5rem)] place-items-center px-6 py-10">
      <section className="flex w-full max-w-md flex-col gap-5 rounded-xl border bg-card p-6 text-card-foreground shadow-sm">
        {me.demo_prompt_dismissed === false ? <DemoChoice org={{ id: membership.org.id, slug: membership.org.slug }} onDismissed={() => navigate(`/app/${membership.org.slug}/sites`)} /> : null}
        <div className="flex flex-col gap-1"><h1 className="text-2xl font-semibold tracking-tight">Create your first site</h1><p className="text-sm text-muted-foreground">Add the website where Supermetry should collect analytics.</p></div>
        <SiteForm
          submitLabel="Create site"
          onSubmit={async (input) => {
            const site = await (createSite ?? ((value) => createManagedSite(value, membership.org.id)))(input)
            navigate(`/app/${membership.org.slug}/${site.public_id}/settings/installation`)
          }}
          secondaryAction={me.demo_prompt_dismissed !== false ? <Button type="button" variant="ghost" onClick={() => navigate(`/app/${membership.org.slug}/sites`)}>Skip for now</Button> : undefined}
        />
      </section>
    </main>
  )
}
