import { isAnonymousTracking } from '@/lib/tracking-policy'
import { anonymousRouteRestricted } from './AnalyticsPrivacyBoundary'
import { hasCapability, type Authorization } from '@/lib/authorization'
import {
  ActivityIcon,
  Building2Icon,
  ChartNoAxesCombinedIcon,
  CircleDollarSignIcon,
  CircleGaugeIcon,
  CodeXmlIcon,
  FilterIcon,
  GoalIcon,
  Globe2Icon,
  GaugeIcon,
  Link2Icon,
  MousePointer2Icon,
  PlusIcon,
  RouteIcon,
  Rows3Icon,
  SettingsIcon,
  TablePropertiesIcon,
  UserRoundIcon,
  UsersRoundIcon,
} from 'lucide-react'
import { Link, useRouterState } from '@tanstack/react-router'

import { SiteIcon } from '@/components/SiteIcon'
import { BrandLogo } from '@/components/BrandLogo'
import { Badge } from '@/components/ui/badge'
import { ReplayNavLink } from '@/components/replay/ReplayNavLink'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'
import { matchesSite, siteSlug } from '@/lib/matchSite'
import type { Me, Membership, Site } from '@/lib/me'
import { en } from '@/messages/en'
import { useCreateDemo } from '@/features/demo/useDemo'
import { ui } from '@/messages/ui'
import { AccountMenu } from './HeaderMenus'
import { defaultSearch, stringifySearch } from '@/lib/search'

export type Role = 'owner' | 'admin' | 'editor' | 'viewer'

const coreNavigation = [
  { get label() { return en.navigation.dashboard }, suffix: '', icon: ChartNoAxesCombinedIcon, scope: 'site' },
  { get label() { return en.navigation.events }, suffix: '/events', icon: CodeXmlIcon, scope: 'site' },
  { get label() { return en.navigation.sessions }, suffix: '/sessions', icon: Rows3Icon, scope: 'site' },
  { get label() { return en.navigation.realtime }, suffix: '/realtime', icon: ActivityIcon, scope: 'site' },
  { get label() { return en.navigation.journeys }, suffix: '/journeys', icon: RouteIcon, scope: 'site' },
  { get label() { return en.navigation.retention }, suffix: '/retention', icon: TablePropertiesIcon, scope: 'site' },
  { get label() { return en.navigation.profiles }, suffix: '/profiles', icon: UserRoundIcon, scope: 'site' },
  { get label() { return en.navigation.cohorts }, suffix: '/cohorts', icon: UsersRoundIcon, scope: 'site' },
  { get label() { return en.navigation.heatmaps }, suffix: '/heatmaps', icon: MousePointer2Icon, scope: 'site' },
  { get label() { return en.navigation.vitals }, suffix: '/vitals', icon: GaugeIcon, scope: 'site' },
  { get label() { return en.navigation.funnels }, suffix: '/funnels', icon: FilterIcon, scope: 'site' },
  { get label() { return en.navigation.goals }, suffix: '/goals', icon: GoalIcon, scope: 'site' },
  { get label() { return en.navigation.revenue }, suffix: '/revenue', icon: CircleDollarSignIcon, scope: 'site' },
] as const

const managementNavigation = [
  { get label() { return en.navigation.sites }, suffix: '/sites', icon: Globe2Icon, scope: 'org' },
  { get label() { return en.navigation.health }, suffix: '/health', icon: CircleGaugeIcon, scope: 'org' },
  { get label() { return en.linksUi.nav }, suffix: '/links', icon: Link2Icon, scope: 'site' },
] as const

export type NavigationItem =
  | (typeof coreNavigation)[number]
  | (typeof managementNavigation)[number]

const navigationSections = [
  { key: 'analyze', get label() { return en.a11y2.sidebarSections.analyze }, suffixes: ['', '/events', '/sessions', '/realtime'] },
  { key: 'behaviour', get label() { return en.a11y2.sidebarSections.behaviour }, suffixes: ['/journeys', '/retention', '/profiles', '/cohorts', '/heatmaps', '/vitals'] },
  { key: 'conversion', get label() { return en.a11y2.sidebarSections.conversion }, suffixes: ['/funnels', '/goals', '/revenue'] },
  { key: 'manage', get label() { return en.a11y2.sidebarSections.manage }, suffixes: ['/sites', '/links', '/health'] },
] as const

export function visibleNavigation(role: Role, hasSite = true, authorization?: Authorization, site?: Site): NavigationItem[] {
  const items: NavigationItem[] = [...coreNavigation, managementNavigation[0], managementNavigation[2]]
  if (role === 'owner' || role === 'admin') items.push(managementNavigation[1])
  return items.filter((item) => (hasSite || item.scope === 'org') &&
    (item.scope === 'org' || !isAnonymousTracking(site) || !anonymousRouteRestricted(`/app/org/site${item.suffix}`)) && ((item.suffix !== '/profiles' && item.suffix !== '/cohorts') || hasCapability(authorization, 'profiles.read')))
}

/** Replays need a site that is not anonymous-tracking and the replay.read capability. */
export function replaysAvailable(authorization: Authorization | undefined, site: Site | undefined): boolean {
  return Boolean(site && !isAnonymousTracking(site) && hasCapability(authorization, 'replay.read'))
}

export function canCreateSites(role: Role): boolean {
  return role === 'owner' || role === 'admin'
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

/** Search params each sidebar link carries. The palette uses the same table, so both land on identical URLs. */
const navigationSearches = {
  '': () => defaultSearch(),
  '/events': () => ({ ...defaultSearch(), name: '', property: '' }),
  '/sessions': () => defaultSearch(),
  '/journeys': () => ({ ...defaultSearch(), direction: 'start' as const, path: '', steps: 3 }),
  '/retention': () => ({ ...defaultSearch(), interval: 'day' as const, identity: 'anonymous' as const }),
  '/cohorts': () => defaultSearch(),
  '/heatmaps': () => ({ ...defaultSearch(), path: '', device: 'desktop' as const }),
  '/vitals': () => defaultSearch(),
  '/revenue': () => defaultSearch(),
} as const

export function navigationSearch<S extends keyof typeof navigationSearches>(suffix: S): ReturnType<(typeof navigationSearches)[S]> {
  return navigationSearches[suffix]() as ReturnType<(typeof navigationSearches)[S]>
}

const dashboardSearch = navigationSearches['']

/** The full URL (path and query string) of a sidebar item, as the router renders its link. */
export function navigationUrl(item: NavigationItem, org: string, site: string): string {
  const search = item.suffix in navigationSearches ? navigationSearches[item.suffix as keyof typeof navigationSearches]() : undefined
  return navigationHref(item, org, site) + (search ? stringifySearch(search) : '')
}

export function siteDashboardUrl(org: string, site: string): string {
  return `/app/${org}/${site}${stringifySearch(dashboardSearch())}`
}

/** Where the organization list entries lead: the first site's dashboard with its search, else the organization settings. */
export function organizationUrl(membership: Membership): string {
  const site = membership.sites[0]
  return site ? siteDashboardUrl(membership.org.slug, siteSlug(site)) : organizationSettingsHref(membership.org.slug)
}

export function replaysUrl(org: string, site: string): string {
  return `/app/${org}/${site}/replays${stringifySearch(defaultSearch())}`
}

export function siteSettingsHref(org: string, site: string): string {
  return `/app/${org}/${site}/settings/general`
}

export function organizationSettingsHref(org: string): string {
  return `/app/${org}/settings`
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

export { matchesSite, siteSlug }

export function activeSite(membership: Membership, requested?: string): Site | undefined {
  return membership.sites.find((site) => matchesSite(site, requested)) ?? membership.sites[0]
}

export type SettingsDestination = 'site' | 'org'

/** Settings links at the bottom of the sidebar. Site settings need a site and a role above viewer. */
export function settingsDestinations(role: Role, site: Site | undefined): SettingsDestination[] {
  return site && role !== 'viewer' ? ['site', 'org'] : ['org']
}

export function settingsIsActive(destination: SettingsDestination, pathname: string, org: string, site?: Site): boolean {
  const [, app, pathOrg, scope, section] = pathname.split('/')
  if (app !== 'app' || pathOrg !== org) return false
  if (destination === 'org') return scope === 'settings'
  return site !== undefined && matchesSite(site, scope) && section === 'settings'
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
          search={navigationSearch('')}
          {...props}
        >
          {content}
        </Link>
      )
    case '/funnels':
      return <Link to="/app/$org/$site/funnels" params={{ org, site }} {...props}>{content}</Link>
    case '/events':
      return <Link to="/app/$org/$site/events" params={{ org, site }} search={navigationSearch('/events')} {...props}>{content}</Link>
    case '/sessions':
      return <Link to="/app/$org/$site/sessions" params={{ org, site }} search={navigationSearch('/sessions')} {...props}>{content}</Link>
    case '/journeys':
      return <Link to="/app/$org/$site/journeys" params={{ org, site }} search={navigationSearch('/journeys')} {...props}>{content}</Link>
    case '/retention':
      return <Link to="/app/$org/$site/retention" params={{ org, site }} search={navigationSearch('/retention')} {...props}>{content}</Link>
    case '/profiles':
      return <Link to="/app/$org/$site/profiles" params={{ org, site }} {...props}>{content}</Link>
    case '/cohorts':
      return <Link to="/app/$org/$site/cohorts" params={{ org, site }} search={navigationSearch('/cohorts')} {...props}>{content}</Link>
    case '/heatmaps':
      return <Link to="/app/$org/$site/heatmaps" params={{ org, site }} search={navigationSearch('/heatmaps')} {...props}>{content}</Link>
    case '/vitals':
      return (
        <Link
          to="/app/$org/$site/vitals"
          params={{ org, site }}
          search={navigationSearch('/vitals')}
          {...props}
        >
          {content}
        </Link>
      )
    case '/realtime':
      return <Link to="/app/$org/$site/realtime" params={{ org, site }} {...props}>{content}</Link>
    case '/goals':
      return <Link to="/app/$org/$site/goals" params={{ org, site }} {...props}>{content}</Link>
    case '/revenue':
      return <Link to="/app/$org/$site/revenue" params={{ org, site }} search={navigationSearch('/revenue')} {...props}>{content}</Link>
    case '/sites':
      return <Link to="/app/$org/sites" params={{ org }} {...props}>{content}</Link>
    case '/links':
      return <Link to="/app/$org/$site/links" params={{ org, site }} {...props}>{content}</Link>
    case '/health':
      return <Link to="/app/$org/health" params={{ org }} {...props}>{content}</Link>
  }

  const unreachable: never = item
  return unreachable
}

/** The brand link at the top of every sidebar. It leads to the sites overview. */
export function SidebarBrandLink({ onClick }: { onClick: () => void }) {
  return (
    <Link aria-label="Supermetry home" className="mb-1 flex h-8 items-center gap-2 px-2 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0" to="/app" onClick={onClick}>
      <BrandLogo className="h-7 w-7" decorative mark />
      <span className="truncate text-base font-semibold group-data-[collapsible=icon]:hidden">{en.navigation.brand}</span>
    </Link>
  )
}

/** The organisations part of the switcher: every membership, then "Add organization". */
export function OrganizationMenuGroup({ me, onSelect }: { me: Me; onSelect: () => void }) {
  return (
    <>
      <DropdownMenuLabel>{en.navigation.organizations}</DropdownMenuLabel>
      <DropdownMenuGroup>
        {me.memberships.map((item) => (
          <DropdownMenuItem key={item.org.id} asChild>
            {item.sites[0] ? (
              <Link
                to="/app/$org/$site"
                params={{ org: item.org.slug, site: siteSlug(item.sites[0]) }}
                search={dashboardSearch()}
                onClick={onSelect}
              >
                {item.org.name}
              </Link>
            ) : (
              <Link
                to="/app/$org/settings"
                params={{ org: item.org.slug }}
                onClick={onSelect}
              >
                {item.org.name}
              </Link>
            )}
          </DropdownMenuItem>
        ))}
        <DropdownMenuItem asChild>
          <Link to="/app/account/organizations/new" onClick={onSelect}>
            <PlusIcon />
            {en.navigation.addOrganization}
          </Link>
        </DropdownMenuItem>
      </DropdownMenuGroup>
    </>
  )
}

/** The account menu in the footer of both sidebars. */
export function SidebarAccountFooter({ me }: { me: Me }) {
  const { isMobile } = useSidebar()
  return (
    <SidebarFooter>
      <SidebarMenu>
        <SidebarMenuItem>
          <AccountMenu
            align="end"
            me={me}
            side={isMobile ? 'top' : 'right'}
            trigger={
              <SidebarMenuButton className="h-11" tooltip={ui.menus.account}>
                <UserRoundIcon aria-hidden="true" />
                <span>{ui.menus.account}</span>
              </SidebarMenuButton>
            }
          />
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarFooter>
  )
}

export function SupermetrySidebar({
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
  const hasDemo = membership.sites.some((item) => item.is_demo)
  const demoStateKnown = membership.sites.every((item) => item.is_demo !== undefined)
  const visibleItems = visibleNavigation(membership.role, Boolean(site), membership, site)
  const showReplays = replaysAvailable(membership, site)

  return (
    <Sidebar aria-label={en.a11y2.applicationSidebar} collapsible="icon" role="complementary">
      <SidebarHeader>
        <SidebarBrandLink onClick={closeMobile} />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton className="h-auto py-1.5 group-data-[collapsible=icon]:p-1.5!" tooltip={`${membership.org.name} / ${site?.name ?? en.navigation.site}`}>
              {site ? <SiteIcon iconUrl={site.icon_url} name={site.name} size={20} /> : null}
              <span className="flex min-w-0 flex-col text-left leading-tight">
                <span className="truncate">{site?.name ?? en.navigation.site}</span>
                <span className="truncate text-xs text-muted-foreground">{membership.org.name}</span>
              </span>
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="min-w-56">
            <DropdownMenuLabel>{en.navigation.projects}</DropdownMenuLabel>
            <DropdownMenuGroup>
              {membership.sites.map((item) => (
                <DropdownMenuItem key={item.id} asChild>
                  <Link
                    to="/app/$org/$site"
                    params={{ org: membership.org.slug, site: siteSlug(item) }}
                    search={dashboardSearch()}
                    onClick={closeMobile}
                  >
                    <SiteIcon iconUrl={item.icon_url} name={item.name} size={20} />
                    <span className="min-w-0 flex-1 truncate">{item.name}</span>
                    {item.is_demo ? <Badge variant="secondary">{ui.sidebar.demo}</Badge> : null}
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
              {canCreateSites(membership.role) && demoStateKnown && !hasDemo ? (
                <AddDemoProjectItem membership={membership} />
              ) : null}
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <OrganizationMenuGroup me={me} onSelect={closeMobile} />
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarHeader>
      <SidebarContent className="group-data-[collapsible=icon]:overflow-auto">
        {navigationSections.map((section) => {
          const suffixes = new Set<string>(section.suffixes)
          const items = visibleItems.filter((item) => suffixes.has(item.suffix))
          if (items.length === 0 && !(section.key === 'analyze' && showReplays)) return null
          const headingId = `sidebar-${section.key}-heading`
          return (
            <SidebarGroup key={section.key}>
              <SidebarGroupLabel asChild><h2 id={headingId}>{section.label}</h2></SidebarGroupLabel>
              <SidebarGroupContent>
                <nav aria-labelledby={headingId}>
                  <SidebarMenu>
                    {items.flatMap((item) => {
                      const active = navigationIsActive(item, pathname, membership.org.slug, currentSite)
                      const rows = [
                        <SidebarMenuItem key={item.label}>
                          <SidebarMenuButton asChild className="h-11" isActive={active} tooltip={item.label}>
                            {navigationLink(item, membership.org.slug, currentSite, active, closeMobile)}
                          </SidebarMenuButton>
                        </SidebarMenuItem>,
                      ]
                      if (item.suffix === '/realtime' && showReplays) {
                        rows.push(
                          <SidebarMenuItem key="Replays">
                            <SidebarMenuButton
                              asChild
                              className="h-11"
                              isActive={pathname.startsWith(`/app/${membership.org.slug}/${currentSite}/replays`)}
                              tooltip="Replays"
                            >
                              <ReplayNavLink enabled org={membership.org.slug} site={currentSite} onClick={closeMobile} />
                            </SidebarMenuButton>
                          </SidebarMenuItem>,
                        )
                      }
                      return rows
                    })}
                  </SidebarMenu>
                </nav>
              </SidebarGroupContent>
            </SidebarGroup>
          )
        })}
        <SidebarGroup className="mt-auto">
          <SidebarGroupLabel asChild><h2 id="sidebar-settings-heading">{ui.menus.settings}</h2></SidebarGroupLabel>
          <SidebarGroupContent>
            <nav aria-labelledby="sidebar-settings-heading">
              <SidebarMenu>
                {settingsDestinations(membership.role, site).map((destination) => {
                  const active = settingsIsActive(destination, pathname, membership.org.slug, site)
                  const label = destination === 'site' ? ui.menus.siteSettings : ui.menus.organizationSettings
                  return (
                    <SidebarMenuItem key={destination}>
                      <SidebarMenuButton asChild className="h-11" isActive={active} tooltip={label}>
                        {destination === 'site' && site ? (
                          <Link
                            aria-current={active ? 'page' : undefined}
                            onClick={closeMobile}
                            params={{ org: membership.org.slug, site: siteSlug(site) }}
                            to="/app/$org/$site/settings/general"
                          >
                            <SettingsIcon />
                            <span>{label}</span>
                          </Link>
                        ) : (
                          <Link
                            aria-current={active ? 'page' : undefined}
                            onClick={closeMobile}
                            params={{ org: membership.org.slug }}
                            to="/app/$org/settings"
                          >
                            <Building2Icon />
                            <span>{label}</span>
                          </Link>
                        )}
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  )
                })}
              </SidebarMenu>
            </nav>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarAccountFooter me={me} />
    </Sidebar>
  )
}

function AddDemoProjectItem({ membership }: { membership: Membership }) {
  const createDemo = useCreateDemo({ id: membership.org.id, slug: membership.org.slug })
  return (
    <DropdownMenuItem
      disabled={createDemo.isPending}
      onSelect={() => createDemo.mutate()}
    >
      <PlusIcon />
      {createDemo.isPending ? 'Adding demo project…' : 'Add demo project'}
    </DropdownMenuItem>
  )
}
