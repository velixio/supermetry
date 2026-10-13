import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'

import { SiteForm } from '@/features/sites/SiteForm'
import { createSite } from '@/features/sites/api'
import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/sites/new')({ component: NewSiteRoute })

function NewSiteRoute() {
  const { org } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe()
  const queryClient = useQueryClient()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)

  if (!membership || (membership.role !== 'owner' && membership.role !== 'admin')) {
    return <p role="alert" className="text-sm text-muted-foreground">You do not have permission to add sites.</p>
  }

  return (
    <PageContainer width="narrow">
      <PageHeader title="Add site" description="Set the domains, local time, and raw-data retention for this site." />
      <section className="rounded-xl border bg-card p-5">
        <SiteForm
          submitLabel="Add site"
          onSubmit={async (input) => {
            const site = await createSite(input, membership.org.id)
            await queryClient.invalidateQueries({ queryKey: ['me'] })
            await navigate({
              to: '/app/$org/$site/settings/installation',
              params: { org, site: site.public_id },
            })
          }}
        />
      </section>
    </PageContainer>
  )
}
