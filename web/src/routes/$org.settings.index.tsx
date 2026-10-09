import { createFileRoute, Link } from '@tanstack/react-router'
import { useOrganization } from '@/features/settings/routeContext'

export const Route = createFileRoute('/$org/settings/')({
  component: OrganizationOverview,
})

function OrganizationOverview() {
  const { org } = Route.useParams()
  const membership = useOrganization(org)
  if (!membership) return null
  return (
    <section className="flex max-w-2xl flex-col gap-4">
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
        <Link className="text-sm underline" to="/$org/sites" params={{ org }}>
          View your sites
        </Link>
      )}
      <Link className="text-sm underline" to="/account/profile">
        Account settings
      </Link>
    </section>
  )
}
