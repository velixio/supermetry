import { Link } from '@tanstack/react-router'

import { AppShell } from '@/components/AppShell'
import { organizationHref } from '@/components/Sidebar'
import { useMe, type Me } from '@/lib/me'
import { en } from '@/messages/en'
import { Route } from '@/routes/app.$org'

export function OrganizationLayout() {
  const { org } = Route.useParams()
  const me = useMe()

  if (!me.data) {
    return (
      <main className="grid min-h-dvh place-items-center px-6">
        <p className="text-sm text-muted-foreground">{en.loadingDashboard}</p>
      </main>
    )
  }

  const membership = me.data.memberships.find((item) => item.org.slug === org)
  if (!membership) return <OrganizationNotFound me={me.data} />
  return <AppShell me={me.data} membership={membership} />
}

function OrganizationNotFound({ me }: { me: Me }) {
  return (
    <main className="grid min-h-dvh place-items-center px-6 py-12">
      <section className="flex w-full max-w-lg flex-col gap-5 border-l-2 border-accent pl-5">
        <div className="flex flex-col gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">Not found</h1>
          <p className="text-sm text-muted-foreground">We could not find that organisation</p>
        </div>
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
      </section>
    </main>
  )
}
