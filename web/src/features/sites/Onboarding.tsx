import { useEffect } from 'react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
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

  if (!membership) {
    return (
      <PageContainer width="narrow">
        <PageHeader title="No organization access" description="Ask an organization owner for an invitation. Your account settings are available from the Account menu." />
      </PageContainer>
    )
  }

  if (existingSite || !canCreateSites(membership.role)) return <p className="text-sm text-muted-foreground" role="status">{en.loadingDashboard}</p>

  // Inside the shell's main: no viewport-height math, the card sits centred below the 44px header.
  return (
    <div className="flex justify-center py-6 md:py-10">
      <section className="flex w-full max-w-md flex-col gap-5 rounded-xl border bg-card p-6 text-card-foreground shadow-sm">
        {me.demo_prompt_dismissed === false ? <DemoChoice org={{ id: membership.org.id, slug: membership.org.slug }} onDismissed={() => navigate(`/app/${membership.org.slug}/sites`)} /> : null}
        <PageHeader title="Create your first site" description="Add the website where Supermetry should collect analytics." />
        <SiteForm
          submitLabel="Create site"
          onSubmit={async (input) => {
            const site = await (createSite ?? ((value) => createManagedSite(value, membership.org.id)))(input)
            navigate(`/app/${membership.org.slug}/${site.public_id}/settings/installation`)
          }}
          secondaryAction={me.demo_prompt_dismissed !== false ? <Button type="button" variant="ghost" onClick={() => navigate(`/app/${membership.org.slug}/sites`)}>Skip for now</Button> : undefined}
        />
      </section>
    </div>
  )
}
