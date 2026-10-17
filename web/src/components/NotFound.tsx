import { createContext, useContext, useEffect, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { CompassIcon, TriangleAlertIcon } from 'lucide-react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { SupportLink } from '@/components/SupportLink'
import { Button } from '@/components/ui/button'
import { useMe, type Membership } from '@/lib/me'
import { uiFlows } from '@/messages/uiFlows'

/** True inside the app shell, which already provides the page's main landmark. */
export const InAppShell = createContext(false)

/** The router's not-found and error fallbacks; outside the shell it brings its own main landmark. */
function StandalonePage({ icon, title, body, children }: { icon: React.ReactNode; title: string; body: string; children: React.ReactNode }) {
  const inShell = useContext(InAppShell)
  const Wrapper = inShell ? 'div' : 'main'
  return (
    <Wrapper {...(inShell ? {} : { id: 'main-content', tabIndex: -1 })} className={inShell ? 'grid place-items-center px-5 py-12' : 'grid min-h-dvh place-items-center bg-background px-5 py-12 text-foreground'}>
      <section className="flex w-full max-w-md flex-col items-start gap-4">
        <span aria-hidden="true" className="grid size-10 place-items-center rounded-full bg-muted text-muted-foreground">{icon}</span>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="text-sm text-muted-foreground">{body}</p>
        <div className="flex flex-wrap gap-2">{children}</div>
        <SupportLink />
      </section>
    </Wrapper>
  )
}

export function NotFoundPage() {
  return (
    <StandalonePage icon={<CompassIcon className="size-5" />} title={uiFlows.errors.notFoundTitle} body={uiFlows.errors.notFoundBody}>
      <Button asChild><Link to="/app">{uiFlows.errors.goHome}</Link></Button>
    </StandalonePage>
  )
}

/** The root error fallback. The raw error stays in the console; the page explains what to do next. */
export function RouteErrorPage({ error, reset }: { error: unknown; reset?: () => void }) {
  useEffect(() => {
    console.error(error)
  }, [error])
  return (
    <StandalonePage icon={<TriangleAlertIcon className="size-5" />} title={uiFlows.errors.errorTitle} body={uiFlows.errors.errorBody}>
      {reset ? <Button type="button" onClick={reset}>{uiFlows.common.retry}</Button> : null}
      {/* A full load leaves a broken client state behind. */}
      <Button asChild variant="outline"><a href="/app">{uiFlows.errors.goHome}</a></Button>
    </StandalonePage>
  )
}

function siteHref(membership: Membership, site: Membership['sites'][number]) {
  return `/app/${membership.org.slug}/${site.slug ?? site.public_id ?? String(site.id)}`
}

/**
 * Shown inside the app shell when the URL names a site the account cannot see. The account is
 * reloaded once first, so a site created a moment ago is never reported missing.
 */
export function UnknownSite({ membership }: { membership: Membership }) {
  const me = useMe()
  const [checked, setChecked] = useState(false)
  const started = useRef(false)
  useEffect(() => {
    if (started.current) return
    started.current = true
    void me.refetch().finally(() => setChecked(true))
  }, [me])
  if (!checked) return <p role="status" className="text-sm text-muted-foreground">{uiFlows.errors.loadingSite}</p>
  return (
    <PageContainer width="narrow">
      <PageHeader title={uiFlows.errors.siteNotFoundTitle} description={uiFlows.errors.siteNotFoundBody} />
      {membership.sites.length > 0 ? (
        <nav aria-label={uiFlows.errors.yourSites} className="flex flex-col gap-2">
          <h2 className="text-sm font-medium">{uiFlows.errors.yourSites}</h2>
          <ul className="flex flex-col gap-1.5">
            {membership.sites.map((site) => (
              <li key={site.id}><Link className="text-sm text-primary underline-offset-4 hover:underline" to={siteHref(membership, site)}>{site.name}</Link></li>
            ))}
          </ul>
        </nav>
      ) : null}
      <div className="mt-4 flex flex-wrap gap-2">
        <Button asChild variant="outline"><Link to="/app/$org/sites" params={{ org: membership.org.slug }}>{uiFlows.errors.allSites}</Link></Button>
      </div>
    </PageContainer>
  )
}
