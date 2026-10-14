import { Link, Outlet } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { organizationHref } from '@/components/Sidebar'
import { useMe, type Me } from '@/lib/me'
import { Route } from '@/routes/app.$org'

/** The organization route inside the shell: its pages, or a not-found page for an organization the user cannot open. */
export function OrganizationLayout() {
  const { org } = Route.useParams()
  const me = useMe()
  // AppLayout (routes/app.tsx) shows the loading state before the shell exists.
  if (!me.data) return null
  const membership = me.data.memberships.find((item) => item.org.slug === org)
  if (!membership) return <OrganizationNotFound me={me.data} />
  return <Outlet />
}

function OrganizationNotFound({ me }: { me: Me }) {
  return (
    <PageContainer width="narrow">
      <PageHeader title="Not found" description="We could not find that organisation" />
      {me.memberships.length > 0 ? (
        <nav aria-label="Available organisations">
          <p className="mb-2 text-sm font-medium">Available organisations</p>
          <ul className="flex flex-col gap-2">
            {me.memberships.map((membership) => (
              <li key={membership.org.id}>
                <Link
                  className="text-sm text-primary underline-offset-4 hover:underline"
                  to={organizationHref(membership)}
                >
                  {membership.org.name}
                </Link>
              </li>
            ))}
          </ul>
        </nav>
      ) : null}
      <Link className="text-sm font-medium text-primary underline-offset-4 hover:underline" to="/app">
        Dashboard
      </Link>
    </PageContainer>
  )
}
