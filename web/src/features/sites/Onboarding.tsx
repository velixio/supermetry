import { useEffect, useState } from 'react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { canCreateSites } from '@/components/Sidebar'
import { DemoChoice } from '@/features/demo/DemoChoice'
import { createSite as createManagedSite } from '@/features/sites/api'
import { AppCreateFlow } from '@/features/sites/AppCreateFlow'
import { KindChoice } from '@/features/sites/KindChoice'
import { PixelProjectForm } from '@/features/sites/PixelProjectForm'
import { SiteForm } from '@/features/sites/SiteForm'
import { SiteInstallStep } from '@/features/sites/SiteInstallStep'
import type { ManagedSite, SiteInput, SiteKind } from '@/features/sites/types'
import { en } from '@/messages/en'
import { retentionPolicy, useInstanceConfig } from '@/lib/instanceConfig'
import type { Me } from '@/routes/app.$org'

export function Onboarding({ me, createSite, navigate }: {
  me: Me
  createSite?: (input: SiteInput) => Promise<ManagedSite>
  navigate: (path: string, replace?: boolean) => void
}) {
  const [kind, setKind] = useState<SiteKind>('web')
  // The web site just created: its install step stays on screen until the person leaves it.
  const [created, setCreated] = useState<ManagedSite | null>(null)
  // Onboard in an organization where the user can add sites, not just the first one listed:
  // someone invited as a viewer elsewhere who then signs up should set up their own organization.
  const membership = me.memberships.find((item) => canCreateSites(item.role)) ?? me.memberships[0]
  const config = useInstanceConfig()
  const existingSite = membership?.sites[0]

  useEffect(() => {
    if (!membership || created) return
    if (existingSite) navigate(`/app/${membership.org.slug}/${existingSite.slug ?? existingSite.public_id ?? String(existingSite.id)}`, true)
    else if (!canCreateSites(membership.role)) navigate(`/app/${membership.org.slug}/settings`, true)
  }, [created, existingSite, membership, navigate])

  if (!membership) {
    return (
      <PageContainer width="narrow">
        <PageHeader title="No organization access" description="Ask an organization owner for an invitation. Your account settings are available from the Account menu." />
      </PageContainer>
    )
  }

  if (created) {
    const slug = created.slug ?? created.public_id
    return (
      <div className="flex justify-center py-6 md:py-10">
        <div className="w-full max-w-2xl rounded-lg bg-card p-6 text-card-foreground">
          <SiteInstallStep orgId={membership.org.id} siteId={created.id} publicId={created.public_id ?? String(created.id)} dashboardHref={`/app/${membership.org.slug}/${slug}`} />
        </div>
      </div>
    )
  }

  if (existingSite || !canCreateSites(membership.role)) return <p className="text-sm text-muted-foreground" role="status">{en.loadingDashboard}</p>

  // Inside the shell's main: no viewport-height math, the card sits centred below the 44px header.
  return (
    <div className="flex justify-center py-6 md:py-10">
      <section className="flex w-full max-w-md flex-col gap-5 rounded-lg bg-card p-6 text-card-foreground">
        {me.demo_prompt_dismissed === false ? <DemoChoice org={{ id: membership.org.id, slug: membership.org.slug }} onDismissed={() => navigate(`/app/${membership.org.slug}/sites`)} /> : null}
        <PageHeader title={en.pixels.measureTitle} description="Choose web analytics or count views where JavaScript cannot run." />
        <KindChoice value={kind} onChange={setKind} />
        {kind === 'app' ? (
          <AppCreateFlow
            orgId={membership.org.id}
            createSite={(input) => (createSite ?? ((value) => createManagedSite(value, membership.org.id)))(input)}
            retention={config.data ? retentionPolicy(config.data) : undefined}
            onDone={(site) => navigate(`/app/${membership.org.slug}/${site.slug ?? site.public_id}/settings/app`)}
          />
        ) : kind === 'pixel' ? (
          <PixelProjectForm
            submitLabel={en.pixels.createProject}
            onSubmit={async (input) => {
              const site = await (createSite ?? ((value) => createManagedSite(value, membership.org.id)))(input)
              navigate(`/app/${membership.org.slug}/${site.slug ?? site.public_id}`)
            }}
          />
        ) : (
          <SiteForm
            submitLabel="Create project"
            retention={config.data ? retentionPolicy(config.data) : undefined}
            requireDomain={config.data?.managed === true}
            onSubmit={async (input) => {
              setCreated(await (createSite ?? ((value) => createManagedSite(value, membership.org.id)))(input))
            }}
            secondaryAction={me.demo_prompt_dismissed !== false ? <Button type="button" variant="ghost" onClick={() => navigate(`/app/${membership.org.slug}/sites`)}>Skip for now</Button> : undefined}
          />
        )}
      </section>
    </div>
  )
}
