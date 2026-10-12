import type { Membership } from '@/lib/me'
import { ui } from '@/messages/ui'

export type Crumb = { label: string; to?: string; params?: Record<string, string> }

const labelOf = (segment: string) => ui.crumbs.segments[segment]

/**
 * Org / Site / Section / Detail from a pathname. Pure: no router access, so it is
 * trivial to test. The last crumb is the current page and has no link.
 */
export function buildCrumbs(pathname: string, membership: Membership): Crumb[] {
  const parts = pathname.split('/').filter(Boolean) // ['app', ...]
  if (parts[0] !== 'app') return []
  const rest = parts.slice(1)
  const crumbs: Crumb[] = []

  if (rest[0] === 'account' || rest[0] === 'admin') {
    const base = `/app/${rest[0]}`
    crumbs.push({ label: rest[0] === 'account' ? ui.crumbs.account : ui.crumbs.admin, to: base })
    pushTail(crumbs, rest.slice(1), base)
    return finish(crumbs)
  }

  const [org, second, ...after] = rest
  if (!org) return []
  crumbs.push({ label: membership.org.name, to: '/app/$org/sites', params: { org } })
  if (!second) return finish(crumbs)

  const isOrgPage = labelOf(second) !== undefined
  if (isOrgPage) {
    pushTail(crumbs, [second, ...after], '/app/$org', { org })
    return finish(crumbs)
  }

  const site = membership.sites.find((item) => item.slug === second)
  crumbs.push({ label: site?.name ?? second, to: '/app/$org/$site', params: { org, site: second } })
  pushTail(crumbs, after.length === 0 ? ['dashboard'] : after, '/app/$org/$site', { org, site: second })
  return finish(crumbs)
}

// Each tail crumb links to its own prefix of the path (the last is unlinked by finish()).
function pushTail(crumbs: Crumb[], segments: string[], base: string, params?: Record<string, string>) {
  let previousKnown: string | undefined
  for (const [index, segment] of segments.entries()) {
    const known = labelOf(segment)
    let to = [base, ...segments.slice(0, index + 1)].join('/')
    // The site settings layout has no index route; its first tab is the landing page.
    if (segment === 'settings' && base === '/app/$org/$site') to += '/general'
    if (known) {
      crumbs.push({ label: known, to, ...(params && { params }) })
      previousKnown = segment
    } else if (previousKnown && previousKnown !== 'settings') {
      crumbs.push({ label: ui.crumbs.detail })
    } else {
      crumbs.push({ label: segment, to, ...(params && { params }) })
    }
  }
}

// Only the last crumb is the page; clear any link on it.
function finish(crumbs: Crumb[]): Crumb[] {
  return crumbs.map((crumb, index) => (index === crumbs.length - 1 ? { label: crumb.label } : crumb))
}
