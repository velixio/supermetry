import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'

import { AppCreateFlow } from '@/features/sites/AppCreateFlow'
import { KindChoice } from '@/features/sites/KindChoice'
import { PixelProjectForm } from '@/features/sites/PixelProjectForm'
import { SiteForm } from '@/features/sites/SiteForm'
import { createSite } from '@/features/sites/api'
import type { SiteInput, SiteKind } from '@/features/sites/types'
import { en } from '@/messages/en'
import { retentionPolicy, useInstanceConfig } from '@/lib/instanceConfig'
import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/sites/new')({ component: NewSiteRoute })

function NewSiteRoute() {
  const [kind, setKind] = useState<SiteKind>('web')
  const { org } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe()
  const queryClient = useQueryClient()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const config = useInstanceConfig()

  if (!membership || (membership.role !== 'owner' && membership.role !== 'admin')) {
    return <p role="alert" className="text-sm text-muted-foreground">You do not have permission to add sites.</p>
  }

  async function submit(input: SiteInput) {
    if (!membership) return
    const site = await createSite(input, membership.org.id)
    await queryClient.invalidateQueries({ queryKey: ['me'] })
    await navigate({
      to: input.kind === 'pixel' ? '/app/$org/$site' : '/app/$org/$site/settings/installation',
      params: { org, site: site.slug ?? site.public_id },
      search: input.kind === 'pixel' ? { period: '30d', compare: 'off', metric: 'visitors', f: [] } : undefined,
    })
  }

  return (
    <PageContainer width="narrow">
      <PageHeader title={en.pixels.measureTitle} description="Choose web analytics or count views where JavaScript cannot run." />
      <section className="flex flex-col gap-4 rounded-xl border bg-card p-4 md:p-5">
        <KindChoice value={kind} onChange={setKind} />
        {kind === 'app'
          ? <AppCreateFlow
              orgId={membership.org.id}
              createSite={(input) => createSite(input, membership.org.id)}
              retention={config.data ? retentionPolicy(config.data) : undefined}
              onDone={(site) => {
                void queryClient.invalidateQueries({ queryKey: ['me'] })
                void navigate({ to: '/app/$org/$site/settings/app', params: { org, site: site.slug ?? site.public_id } })
              }}
            />
          : kind === 'pixel'
          ? <PixelProjectForm submitLabel={en.pixels.createProject} onSubmit={submit} />
          : <SiteForm submitLabel="Add site" onSubmit={submit} retention={config.data ? retentionPolicy(config.data) : undefined} requireDomain={config.data?.managed === true} />}
      </section>
    </PageContainer>
  )
}
