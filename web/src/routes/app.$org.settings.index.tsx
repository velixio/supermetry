import { createFileRoute, Link } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { useOrganization } from '@/features/settings/routeContext'
import { DeleteOrgDialog } from '@/features/lifecycle/DeleteOrgDialog'
import { OrgNameForm } from '@/features/lifecycle/OrgNameForm'
import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/settings/')({
  component: OrganizationOverview,
})

function OrganizationOverview() {
  const { org } = Route.useParams()
  const membership = useOrganization(org)
  const me = useMe()
  const navigate = Route.useNavigate()
  const queryClient = useQueryClient()
  if (!membership) return null
  return (
    <section className="flex max-w-2xl flex-col gap-6">
      <header>
        <h1 className="text-2xl font-semibold">{membership.org.name}</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Your organization access
        </p>
      </header>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 rounded-xl border bg-card px-4 py-3 text-sm">
      <p>
        Role: <span className="font-medium capitalize">{membership.role}</span>
      </p>
      {!membership.sites.length ? (
        <p className="text-sm text-muted-foreground">
          No project data access. An organization owner or admin can assign
          sites to your membership.
        </p>
      ) : (
        <Link className="text-primary underline-offset-4 hover:underline" to="/app/$org/sites" params={{ org }}>
          View your sites
        </Link>
      )}
      <Link className="text-primary underline-offset-4 hover:underline" to="/app/account/profile">
        Account settings
      </Link>
      </div>
      <OrgNameForm
        orgId={membership.org.id}
        name={membership.org.name}
        role={membership.role}
      />
      {membership.role === 'owner' ? (
        <DeleteOrgDialog
          orgId={membership.org.id}
          orgName={membership.org.name}
          hasNonDemoSites={membership.sites.some((site) => !site.is_demo)}
          totpEnabled={Boolean(me.data?.user.totp_enabled)}
          navigate={(path) => {
            void queryClient.invalidateQueries({ queryKey: ['me'] })
            void navigate({ to: path })
          }}
        />
      ) : null}
    </section>
  )
}
