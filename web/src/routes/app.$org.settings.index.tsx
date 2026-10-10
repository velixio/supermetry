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
      <p className="text-sm">
        Role: <span className="capitalize">{membership.role}</span>
      </p>
      {!membership.sites.length ? (
        <p className="text-sm text-muted-foreground">
          No project data access. An organization owner or admin can assign
          sites to your membership.
        </p>
      ) : (
        <Link className="text-sm underline" to="/app/$org/sites" params={{ org }}>
          View your sites
        </Link>
      )}
      <Link className="text-sm underline" to="/app/account/profile">
        Account settings
      </Link>
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
