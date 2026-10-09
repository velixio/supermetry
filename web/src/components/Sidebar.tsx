import { isAnonymousTracking } from '@/lib/tracking-policy'
import { anonymousRouteRestricted } from './AnalyticsPrivacyBoundary'
import { hasCapability, type Authorization } from '@/lib/authorization'
import { Fragment } from 'react'
import {
  ActivityIcon,
  ChartNoAxesCombinedIcon,
  CircleGaugeIcon,
  CodeXmlIcon,
  FilterIcon,
  GoalIcon,
  Globe2Icon,
  GaugeIcon,
  MousePointer2Icon,
  PlusIcon,
  RouteIcon,
  Rows3Icon,
  TablePropertiesIcon,
  UserRoundIcon,
} from 'lucide-react'
import { Link, useRouterState } from '@tanstack/react-router'

import { SiteIcon } from '@/components/SiteIcon'
import { ReplayNavLink } from '@/components/replay/ReplayNavLink'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'
import type { Me, Membership, Site } from '@/routes/app.$org'
import { en } from '@/messages/en'

export type Role = 'owner' | 'admin' | 'editor' | 'viewer'

const coreNavigation = [
  { label: en.navigation.dashboard, suffix: '', icon: ChartNoAxesCombinedIcon, scope: 'site' },
  { label: en.navigation.events, suffix: '/events', icon: CodeXmlIcon, scope: 'site' },
  { label: en.navigation.sessions, suffix: '/sessions', icon: Rows3Icon, scope: 'site' },
  { label: en.navigation.realtime, suffix: '/realtime', icon: ActivityIcon, scope: 'site' },
  { label: en.navigation.journeys, suffix: '/journeys', icon: RouteIcon, scope: 'site' },
  { label: en.navigation.retention, suffix: '/retention', icon: TablePropertiesIcon, scope: 'site' },
  { label: en.navigation.profiles, suffix: '/profiles', icon: UserRoundIcon, scope: 'site' },
  { label: en.navigation.heatmaps, suffix: '/heatmaps', icon: MousePointer2Icon, scope: 'site' },
  { label: en.navigation.vitals, suffix: '/vitals', icon: GaugeIcon, scope: 'site' },
  { label: en.navigation.funnels, suffix: '/funnels', icon: FilterIcon, scope: 'site' },
  { label: en.navigation.goals, suffix: '/goals', icon: GoalIcon, scope: 'site' },
] as const

const managementNavigation = [
  { label: en.navigation.sites, suffix: '/sites', icon: Globe2Icon, scope: 'org' },
  { label: en.navigation.health, suffix: '/health', icon: CircleGaugeIcon, scope: 'org' },
] as const

export type NavigationItem =
  | (typeof coreNavigation)[number]
  | (typeof managementNavigation)[number]

export function visibleNavigation(role: Role, hasSite = true, authorization?: Authorization, site?: Site): NavigationItem[] {
  const items: NavigationItem[] = [...coreNavigation, managementNavigation[0]]
  if (role === 'owner' || role === 'admin') items.push(managementNavigation[1])
  return items.filter((item) => (hasSite || item.scope === 'org') &&
    (item.scope === 'org' || !isAnonymousTracking(site) || !anonymousRouteRestricted(`/app/org/site${item.suffix}`)) && (item.suffix !== '/profiles' || hasCapability(authorization, 'profiles.read')))
}

export function canCreateSites(role: Role): boolean {
  return role === 'owner' || role === 'admin'
}

export function siteSlug(site: Site): string {
  return site.slug ?? site.public_id ?? String(site.id)
}

export function organizationHref(membership: Membership): string {
  const site = membership.sites[0]
  return site
    ? `/app/${membership.org.slug}/${siteSlug(site)}`
    : `/app/${membership.org.slug}/settings`
}

export function navigationHref(
  item: NavigationItem,
  org: string,
  site: string,
): string {
  const base = item.scope === 'org' ? `/app/${org}` : `/app/${org}/${site}`
  return `${base}${item.suffix}`
}

export function navigationIsActive(
  item: NavigationItem,
  pathname: string,
  org: string,
  site: string,
): boolean {
  const href = navigationHref(item, org, site)
  if (item.suffix === '') return pathname === href || pathname === `${href}/`
  return pathname === href || pathname.startsWith(`${href}/`)
}

export function matchesSite(site: Site, requested?: string): boolean {
  return requested !== undefined && [site.slug, site.public_id, String(site.id)].includes(requested)
}

export function activeSite(membership: Membership, requested?: string): Site | undefined {
  return membership.sites.find((site) => matchesSite(site, requested)) ?? membership.sites[0]
}

function navigationLink(
  item: NavigationItem,
  org: string,
  site: string,
  active: boolean,
  closeMobile: () => void,
) {
  const props = {
    'aria-current': active ? ('page' as const) : undefined,
    onClick: closeMobile,
  }
  const content = (
    <>
      <item.icon />
      <span>{item.label}</span>
    </>
  )

  switch (item.suffix) {
    case '':
      return (
        <Link
          to="/app/$org/$site"
          params={{ org, site }}
          search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true }}
          {...props}
        >
          {content}
        </Link>
      )
    case '/funnels':
      return <Link to="/app/$org/$site/funnels" params={{ org, site }} {...props}>{content}</Link>
    case '/events':
      return <Link to="/app/$org/$site/events" params={{ org, site }} search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true, name: '', property: '' }} {...props}>{content}</Link>
    case '/sessions':
      return <Link to="/app/$org/$site/sessions" params={{ org, site }} search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true }} {...props}>{content}</Link>
    case '/journeys':
      return <Link to="/app/$org/$site/journeys" params={{ org, site }} search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true, direction: 'start', path: '', steps: 3 }} {...props}>{content}</Link>
    case '/retention':
      return <Link to="/app/$org/$site/retention" params={{ org, site }} search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true, interval: 'day', identity: 'anonymous' }} {...props}>{content}</Link>
    case '/profiles':
      return <Link to="/app/$org/$site/profiles" params={{ org, site }} {...props}>{content}</Link>
    case '/heatmaps':
      return <Link to="/app/$org/$site/heatmaps" params={{ org, site }} search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true, path: '', device: 'desktop' }} {...props}>{content}</Link>
    case '/vitals':
      return (
        <Link
          to="/app/$org/$site/vitals"
          params={{ org, site }}
          search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true }}
          {...props}
        >
          {content}
        </Link>
      )
    case '/realtime':
      return <Link to="/app/$org/$site/realtime" params={{ org, site }} {...props}>{content}</Link>
    case '/goals':
      return <Link to="/app/$org/$site/goals" params={{ org, site }} {...props}>{content}</Link>
    case '/sites':
      return <Link to="/app/$org/sites" params={{ org }} {...props}>{content}</Link>
    case '/health':
      return <Link to="/app/$org/health" params={{ org }} {...props}>{content}</Link>
  }

  const unreachable: never = item
  return unreachable
}

export function PanelSidebar({
  me,
  membership,
  requestedSite,
}: {
  me: Me
  membership: Membership
  requestedSite?: string
}) {
  const site = activeSite(membership, requestedSite)
  const currentSite = site ? siteSlug(site) : ''
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const { setOpenMobile } = useSidebar()
  const closeMobile = () => setOpenMobile(false)

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton tooltip={en.navigation.organization}>
              <span className="truncate font-semibold">{membership.org.name}</span>
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuLabel>{en.navigation.organization}</DropdownMenuLabel>
            <DropdownMenuGroup>
              {me.memberships.map((item) => (
                <DropdownMenuItem key={item.org.id} asChild>
                  {item.sites[0] ? (
                    <Link
                      to="/app/$org/$site"
                      params={{ org: item.org.slug, site: siteSlug(item.sites[0]) }}
                      search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true }}
                      onClick={closeMobile}
                    >
                      {item.org.name}
                    </Link>
                  ) : (
                    <Link
                      to="/app/$org/settings"
                      params={{ org: item.org.slug }}
                      onClick={closeMobile}
                    >
                      {item.org.name}
                    </Link>
                  )}
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton tooltip={en.navigation.site}>
              {site ? <SiteIcon iconUrl={site.icon_url} name={site.name} size={20} /> : null}
              <span className="truncate">{site?.name ?? en.navigation.site}</span>
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuLabel>{en.navigation.site}</DropdownMenuLabel>
            <DropdownMenuGroup>
              {membership.sites.map((item) => (
                <DropdownMenuItem key={item.id} asChild>
                  <Link
                    to="/app/$org/$site"
                    params={{ org: membership.org.slug, site: siteSlug(item) }}
                    search={{ period: '30d', compare: 'off', f: [], metric: 'visitors', include_imported: true }}
                    onClick={closeMobile}
                  >
                    <SiteIcon iconUrl={item.icon_url} name={item.name} size={20} />
                    {item.name}
                  </Link>
                </DropdownMenuItem>
              ))}
              {canCreateSites(membership.role) ? (
                <DropdownMenuItem asChild>
                  <Link
                    to="/app/$org/sites/new"
                    params={{ org: membership.org.slug }}
                    onClick={closeMobile}
                  >
                    <PlusIcon />
                    {en.navigation.addSite}
                  </Link>
                </DropdownMenuItem>
              ) : null}
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              {visibleNavigation(membership.role, Boolean(site), membership, site).map((item) => (
                <Fragment key={item.label}>
                  <SidebarMenuItem>
                    <SidebarMenuButton
                      asChild
                      isActive={navigationIsActive(
                        item,
                        pathname,
                        membership.org.slug,
                        currentSite,
                      )}
                      tooltip={item.label}
                    >
                      {navigationLink(
                        item,
                        membership.org.slug,
                        currentSite,
                        navigationIsActive(
                          item,
                          pathname,
                          membership.org.slug,
                          currentSite,
                        ),
                        closeMobile,
                      )}
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                  {item.suffix === '/realtime' && site && !isAnonymousTracking(site) && hasCapability(membership, 'replay.read') ? (
                    <SidebarMenuItem>
                      <SidebarMenuButton
                        asChild
                        isActive={pathname.startsWith(`/app/${membership.org.slug}/${currentSite}/replays`)}
                        tooltip="Replays"
                      >
                        <ReplayNavLink enabled org={membership.org.slug} site={currentSite} onClick={closeMobile} />
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  ) : null}
                </Fragment>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
    </Sidebar>
  )
}
