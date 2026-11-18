import { isAnonymousTracking } from '@/lib/tracking-policy'
import { anonymousRouteRestricted } from './AnalyticsPrivacyBoundary'
import { hasCapability, type Authorization } from '@/lib/authorization'
import {
  ActivityIcon,
  BugIcon,
  Building2Icon,
  ChartNoAxesCombinedIcon,
  CircleDollarSignIcon,
  ChevronsUpDownIcon,
  CircleGaugeIcon,
  CodeXmlIcon,
  MonitorSmartphoneIcon,
  TagIcon,
  FilterIcon,
  GoalIcon,
  Globe2Icon,
  GaugeIcon,
  ScrollTextIcon,
  SquareActivityIcon,
  TimerIcon,
  Link2Icon,
  MousePointer2Icon,
  PinIcon,
  PinOffIcon,
  PlusIcon,
  CheckIcon,
  FlaskConicalIcon,
  RouteIcon,
  Rows3Icon,
  SettingsIcon,
  StarIcon,
  TablePropertiesIcon,
  UserRoundIcon,
  UsersRoundIcon,
} from 'lucide-react'
import { skipToken, useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { Link, useRouter, useRouterState } from '@tanstack/react-router'

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
  SidebarMenuSubButton,
  useSidebar,
} from '@/components/ui/sidebar'
import { settingsGroups } from '@/features/settings/SettingsNav'
import { liveSummaryKey } from '@/features/realtime/liveSummaryKey'
import { useGoTo } from '@/lib/shortcuts'
import { NAV_CHORDS, SETTINGS_CHORD, chordLabel } from './sidebar/chords'
import { useRovingNav } from './sidebar/roving'
import { FILTER_SHORTCUT, filterGroups, flattenRows, NoMatches, SidebarFilterField } from './sidebar/filter'
import { MenuEntry, MenuGroup, optionId, rowClass, subRowClass, type MenuGroupData, type MenuRow } from './sidebar/menu'
import { matchesSite, siteSlug } from '@/lib/matchSite'
import type { Me, Membership, Site } from '@/lib/me'
import { en } from '@/messages/en'
import { useCreateDemo } from '@/features/demo/useDemo'
import { ui } from '@/messages/ui'
import { AccountMenu } from './HeaderMenus'
import { defaultSearch, stringifySearch } from '@/lib/search'
import { useSidebarPrefs } from '@/lib/shellPrefs'
import { isOrgScopedPath } from './routeCrumbs'
import { orgSwitchTarget, siteSwitchTarget, type CurrentRoute } from '@/lib/switchTarget'

export type Role = 'owner' | 'admin' | 'editor' | 'viewer'

const coreNavigation = [
  { get label() { return en.navigation.dashboard }, suffix: '', icon: ChartNoAxesCombinedIcon, scope: 'site' },
  { get label() { return en.navigation.events }, suffix: '/events', icon: CodeXmlIcon, scope: 'site' },
  { get label() { return en.navigation.sessions }, suffix: '/sessions', icon: Rows3Icon, scope: 'site' },
  { get label() { return en.navigation.realtime }, suffix: '/realtime', icon: ActivityIcon, scope: 'site' },
  { get label() { return en.navigation.usersAndSessions }, suffix: '/users', icon: UsersRoundIcon, scope: 'site' },
  { get label() { return en.navigation.issues }, suffix: '/issues', icon: BugIcon, scope: 'site' },
  { get label() { return en.navigation.compatibility }, suffix: '/compatibility', icon: MonitorSmartphoneIcon, scope: 'site' },
  { get label() { return en.navigation.releases }, suffix: '/releases', icon: TagIcon, scope: 'site' },
  { get label() { return en.navigation.performance }, suffix: '/performance', icon: TimerIcon, scope: 'site' },
  { get label() { return en.navigation.logs }, suffix: '/obs/logs', icon: ScrollTextIcon, scope: 'site' },
  { get label() { return en.navigation.metrics }, suffix: '/obs/metrics', icon: SquareActivityIcon, scope: 'site' },
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
  { get label() { return en.githubStars.nav }, suffix: '/github-stars', icon: StarIcon, scope: 'org' },
] as const

export const PIXEL_NAV_SUFFIXES: readonly string[] = ['', '/sites', '/health', '/github-stars']

/** Pages that exist only for app projects. */
const APP_ONLY_SUFFIXES: readonly string[] = ['/issues', '/compatibility', '/releases', '/performance', '/users', '/obs/logs', '/obs/metrics']

export const APP_NAV_SUFFIXES: readonly string[] = ['', '/events', '/users', '/issues', '/compatibility', '/releases', '/performance', '/obs/logs', '/obs/metrics', '/journeys', '/retention', '/funnels', '/sites', '/health', '/settings']

export type NavigationItem =
  | (typeof coreNavigation)[number]
  | (typeof managementNavigation)[number]

const navigationSections = [
  { key: 'analyze', get label() { return en.a11y2.sidebarSections.analyze }, suffixes: ['', '/issues', '/compatibility', '/releases', '/performance', '/obs/logs', '/obs/metrics', '/users', '/events', '/sessions', '/realtime'] },
  { key: 'behaviour', get label() { return en.a11y2.sidebarSections.behaviour }, suffixes: ['/journeys', '/retention', '/profiles', '/cohorts', '/heatmaps', '/vitals'] },
  { key: 'conversion', get label() { return en.a11y2.sidebarSections.conversion }, suffixes: ['/funnels', '/goals', '/revenue'] },
  { key: 'manage', get label() { return en.a11y2.sidebarSections.manage }, suffixes: ['/sites', '/links', '/health', '/github-stars'] },
] as const

/** GitHub stars belongs to the organization, so every member gets the item. */
export function visibleNavigation(role: Role, hasSite = true, authorization?: Authorization, site?: Site): NavigationItem[] {
  const items: NavigationItem[] = [...coreNavigation, managementNavigation[0], managementNavigation[2]]
  if (role === 'owner' || role === 'admin') items.push(managementNavigation[1])
  items.push(managementNavigation[3])
  return items.filter((item) => (hasSite || item.scope === 'org') &&
    (site?.kind !== 'pixel' || PIXEL_NAV_SUFFIXES.includes(item.suffix)) &&
    (site?.kind !== 'app' || APP_NAV_SUFFIXES.includes(item.suffix)) &&
    (site?.kind === 'app' || !APP_ONLY_SUFFIXES.includes(item.suffix)) &&
    (item.scope === 'org' || !isAnonymousTracking(site) || !anonymousRouteRestricted(`/app/org/site${item.suffix}`)) && ((item.suffix !== '/profiles' && item.suffix !== '/cohorts') || hasCapability(authorization, 'profiles.read')))
}

/** Replays need a site that is not anonymous-tracking and the replay.read capability. */
export function replaysAvailable(authorization: Authorization | undefined, site: Site | undefined): boolean {
  return Boolean(site && site.kind !== 'pixel' && site.kind !== 'app' && !isAnonymousTracking(site) && hasCapability(authorization, 'replay.read'))
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
  label: ReactNode = item.label,
  extras: { chord?: string; live?: number } = {},
) {
  const props = {
    'aria-current': active ? ('page' as const) : undefined,
    'aria-description': extras.live ? ui.sidebar.liveNow(extras.live) : undefined,
    onClick: closeMobile,
  }
  const content = (
    <>
      <item.icon />
      <span className="truncate">{label}</span>
      <LiveDot count={extras.live} />
      <ChordHint chord={extras.chord} />
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
    case '/issues':
      return <Link to="/app/$org/$site/issues" params={{ org, site }} {...props}>{content}</Link>
    case '/compatibility':
      return <Link to="/app/$org/$site/compatibility" params={{ org, site }} {...props}>{content}</Link>
    case '/releases':
      return <Link to="/app/$org/$site/releases" params={{ org, site }} {...props}>{content}</Link>
    case '/performance':
      return <Link to="/app/$org/$site/performance" params={{ org, site }} {...props}>{content}</Link>
    case '/obs/logs':
      return <Link to="/app/$org/$site/obs/logs" params={{ org, site }} {...props}>{content}</Link>
    case '/obs/metrics':
      return <Link to="/app/$org/$site/obs/metrics" params={{ org, site }} {...props}>{content}</Link>
    case '/users':
      return <Link to="/app/$org/$site/users" params={{ org, site }} {...props}>{content}</Link>
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
    case '/github-stars':
      return <Link to="/app/$org/github-stars" params={{ org }} {...props}>{content}</Link>
  }

  const unreachable: never = item
  return unreachable
}

/** A small data-colored dot on an icon: something is happening there right now. Decorative; the row carries aria-description. */
function LiveDot({ count }: { count?: number }) {
  if (!count) return null
  return <span aria-hidden="true" className="absolute top-2.5 left-[18px] size-2 rounded-full border-2 border-sidebar bg-chart-1 md:top-1" data-testid="live-dot" />
}

/** The go-to chord, muted and right-aligned, only while the row is hovered or focused. Drawn by CSS so it never enters the row's text. */
function ChordHint({ chord }: { chord?: string }) {
  if (!chord) return null
  return <span aria-hidden="true" className="ml-auto hidden text-[11px] text-muted-foreground before:content-[attr(data-chord)] group-focus-within/menu-item:inline group-hover/menu-item:inline group-data-[collapsible=icon]:hidden" data-chord={chordLabel(chord)} />
}

/** Rail tooltip: the label, then the chord as a small key hint. */
function tipFor(label: string, chord?: string, live?: number) {
  const text = live ? `${label} · ${ui.sidebar.liveNow(live)}` : label
  if (!chord) return text
  return { children: <>{text}<kbd className="ml-2 rounded-sm bg-background/20 px-1 text-[10px] font-medium">{chordLabel(chord)}</kbd></> }
}

/** The brand link at the top of every sidebar. It leads to the sites overview. */
export function SidebarBrandLink({ onClick }: { onClick: () => void }) {
  return (
    <Link aria-label="Supermetry home" className="flex h-8 items-center gap-2 rounded-md px-2 outline-hidden focus-visible:ring-2 focus-visible:ring-ring/50 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0" to="/app" onClick={onClick}>
      <BrandLogo decorative mark size="sm" />
      <span className="truncate text-sm font-semibold group-data-[collapsible=icon]:hidden">{en.navigation.brand}</span>
    </Link>
  )
}

/** The matched leaf route, so a switcher can keep the user on the same page. */
export function useCurrentRoute(): CurrentRoute {
  const routeId = useRouterState({ select: (state) => state.matches[state.matches.length - 1]?.routeId ?? '' })
  const search = useRouterState({ select: (state) => state.location.search as Record<string, unknown> })
  return { routeId, search }
}

/** Small muted section heading inside the switcher menu. */
const switcherLabelClass = 'px-2 pt-2 pb-1 text-xs font-medium text-muted-foreground'

/** The square that holds an icon or initial in a switcher row, so every row lines up on one 20px column. */
function SwitcherGlyph({ children, dashed = false }: { children: ReactNode; dashed?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={`flex size-5 shrink-0 items-center justify-center rounded-md border text-[0.6875rem] font-semibold text-muted-foreground ${dashed ? 'border-dashed' : 'bg-muted'}`}
    >
      {children}
    </span>
  )
}

/** Trailing tick for the row the user is on; screen readers hear "(current)" instead. */
function CurrentMark() {
  return (
    <>
      <CheckIcon aria-hidden="true" className="ml-auto size-4 text-foreground" />
      <span className="sr-only">{ui.sidebar.current}</span>
    </>
  )
}

/** Action rows ("Add site", "Add organization") are muted with a dashed glyph, so they read as commands, not destinations. */
const switcherActionClass = 'text-muted-foreground'

/** The organisations part of the switcher: every membership (the current one ticked), then "Add organization". */
export function OrganizationMenuGroup({ me, onSelect, currentOrgId }: { me: Me; onSelect: () => void; currentOrgId?: number }) {
  const current = useCurrentRoute()
  return (
    <>
      <DropdownMenuLabel className={switcherLabelClass}>{en.navigation.organizations}</DropdownMenuLabel>
      <DropdownMenuGroup>
        {me.memberships.map((item) => {
          // Stay on the same organization-level page when the new organization has it.
          const target = orgSwitchTarget(current, item)
          return (
            <DropdownMenuItem key={item.org.id} asChild>
              <Link to={target.pathname as never} search={target.search as never} onClick={onSelect}>
                <SwitcherGlyph>{item.org.name.trim().charAt(0).toUpperCase()}</SwitcherGlyph>
                <span className="min-w-0 flex-1 truncate">{item.org.name}</span>
                {item.org.id === currentOrgId ? <CurrentMark /> : null}
              </Link>
            </DropdownMenuItem>
          )
        })}
        <DropdownMenuItem asChild className={switcherActionClass}>
          <Link to="/app/account/organizations/new" onClick={onSelect}>
            <SwitcherGlyph dashed><PlusIcon className="size-3.5" /></SwitcherGlyph>
            {en.navigation.addOrganization}
          </Link>
        </DropdownMenuItem>
      </DropdownMenuGroup>
    </>
  )
}

export const githubStarsPath = '/app/github-stars'

/** The account menu in the footer of both sidebars. */
export function SidebarAccountFooter({ me }: { me: Me }) {
  const { isMobile } = useSidebar()
  const name = me.user.name?.trim() || me.user.email
  return (
    <SidebarFooter className="p-2 group-data-[collapsible=icon]:pb-3">
      <SidebarMenu>
        <SidebarMenuItem>
          <AccountMenu
            align="end"
            me={me}
            side={isMobile ? 'top' : 'right'}
            trigger={
              <SidebarMenuButton aria-label={ui.menus.account} className={`${rowClass} group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:p-0!`} tooltip={name}>
                <span aria-hidden="true" className="flex size-5 shrink-0 items-center justify-center rounded-full bg-muted text-[10px] font-semibold text-muted-foreground">
                  {name.charAt(0).toUpperCase()}
                </span>
                <span>{name}</span>
              </SidebarMenuButton>
            }
          />
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarFooter>
  )
}

/** Sub-destinations of project settings, in the order of the settings tabs. Mirrors the tabs on the settings page. */
const siteSettingsPages = ['general', 'installation', 'symbols', 'integrations', 'tracking', 'replay', 'goals', 'funnels', 'shares', 'reports', 'alerts', 'alert-silences', 'monitors', 'notification-history', 'imports', 'app'] as const
const appSettingsPages: readonly string[] = ['general', 'installation', 'symbols', 'alerts', 'alert-silences', 'monitors', 'app']

export const subPageLabel = (page: string): string => ui.sidebar.subPages[page] ?? ui.crumbs.segments[page] ?? page

/** The settings sub-pages offered for a project, following the same rules as the tabs on the settings page. */
export function siteSettingsSubPages(site: Site | undefined, showReplay: boolean): string[] {
  const restricted = isAnonymousTracking(site)
  return siteSettingsPages.filter((page) =>
    (site?.kind === 'app' ? appSettingsPages.includes(page) : page !== 'app') &&
    (page !== 'replay' || showReplay) &&
    (!restricted || !['imports', 'goals', 'funnels'].includes(page)))
}

export function SupermetrySidebar({
  me,
  membership,
  requestedSite,
  resolvedSite,
  onOpenPalette,
}: {
  me: Me
  membership: Membership
  requestedSite?: string
  resolvedSite?: Site
  /** Opens the command palette from "Search everything". Without it the sidebar presses the palette's own shortcut. */
  onOpenPalette?: () => void
}) {
  const site = activeSite(membership, requestedSite)
  const navigationSite = resolvedSite ?? site
  const currentSite = site ? siteSlug(site) : ''
  const org = membership.org.slug
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const router = useRouter()
  // The site list keeps the current page (and its portable search) for the chosen site.
  const current = useCurrentRoute()
  const { setOpenMobile, setOpen, state: sidebarState, isMobile, peek, setPeek } = useSidebar()
  const closeMobile = () => setOpenMobile(false)
  const { prefs, setGroupOpen, togglePin } = useSidebarPrefs(me.user.id)
  const orgScope = isOrgScopedPath(pathname)
  const hasDemo = membership.sites.some((item) => item.is_demo)
  const demoStateKnown = membership.sites.every((item) => item.is_demo !== undefined)
  const visibleItems = visibleNavigation(membership.role, Boolean(navigationSite), membership, navigationSite)
  const showReplays = replaysAvailable(membership, navigationSite)
  // On the icon rail every group shows, so no item is hidden behind a collapsed heading.
  // While the rail is peeking it is drawn at full width, so it behaves like the expanded sidebar.
  const rail = sidebarState === 'collapsed' && !isMobile && !peek
  // The live dot reads the cache the header's live badge fills (same key); it never fetches on its own.
  const liveCount = useQuery<{ active_visitors?: number }>({ queryKey: liveSummaryKey(navigationSite?.id ?? 0), queryFn: skipToken }).data?.active_visitors
  const nav = useRovingNav({ onRailExpand: () => setPeek(true) })

  const [query, setQuery] = useState('')
  const [cursorKey, setCursorKey] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const focusAfterExpand = useRef(false)
  const filtering = query.trim() !== ''

  const sections: MenuGroupData[] = navigationSections.map((section) => {
    const suffixes = new Set<string>(section.suffixes)
    const entries: MenuRow[] = []
    for (const item of visibleItems.filter((candidate) => suffixes.has(candidate.suffix))) {
      const active = navigationIsActive(item, pathname, org, currentSite)
      entries.push({
        key: `nav:${item.suffix || '/'}`,
        label: item.label,
        href: navigationUrl(item, org, currentSite),
        active,
        pinnable: true,
        chord: NAV_CHORDS[item.suffix],
        render: ({ label, id, kbd }) => (
          <SidebarMenuButton asChild className={`${rowClass}${item.suffix === '/realtime' && liveCount ? ' relative' : ''}`} data-kbd={kbd || undefined} data-nav-item="" id={id} isActive={active} tooltip={tipFor(item.label, NAV_CHORDS[item.suffix], item.suffix === '/realtime' ? liveCount : undefined)}>
            {navigationLink(item, org, currentSite, active, closeMobile, label, { chord: NAV_CHORDS[item.suffix], live: item.suffix === '/realtime' ? liveCount : undefined })}
          </SidebarMenuButton>
        ),
      })
      if (item.suffix === '/realtime' && showReplays) {
        const replayActive = pathname.startsWith(`/app/${org}/${currentSite}/replays`)
        entries.push({
          key: 'nav:/replays',
          label: 'Replays',
          href: replaysUrl(org, currentSite),
          active: replayActive,
          pinnable: true,
          chord: NAV_CHORDS['/replays'],
          // ReplayNavLink draws its own fixed label, so the filter match is not highlighted on this row.
          render: ({ id, kbd }) => (
            <SidebarMenuButton asChild className={rowClass} data-kbd={kbd || undefined} data-nav-item="" id={id} isActive={replayActive} tooltip={tipFor('Replays', NAV_CHORDS['/replays'])}>
              <ReplayNavLink enabled org={org} site={currentSite} onClick={closeMobile} />
            </SidebarMenuButton>
          ),
        })
      }
    }
    const label = section.key === 'analyze' && site?.kind === 'app' ? en.a11y2.sidebarSections.appMonitoring : section.label
    return { key: section.key, label, entries }
  })

  // Settings: one collapsible row per destination, with the pages inside it as a second level.
  const subRow = (key: string, label: string, href: string, active: boolean): MenuRow => ({
    key,
    label,
    href,
    active,
    render: ({ label: text, id, kbd }) => (
      <SidebarMenuSubButton asChild className={subRowClass} data-kbd={kbd || undefined} data-nav-item="" id={id} isActive={active}>
        <Link aria-current={active ? 'page' : undefined} onClick={closeMobile} to={href as never}>
          <span>{text}</span>
        </Link>
      </SidebarMenuSubButton>
    ),
  })
  const managers = membership.role === 'owner' || membership.role === 'admin'
  const settingsList = settingsDestinations(membership.role, site)
  const settingsEntries: MenuRow[] = settingsList.map((destination) => {
    // `g ,` goes to project settings when the user has them, else to organization settings.
    const settingsChord = destination === 'site' || !settingsList.includes('site') ? SETTINGS_CHORD : undefined
    const active = settingsIsActive(destination, pathname, org, site)
    const isSite = destination === 'site' && site
    const label = isSite ? ui.menus.siteSettings : ui.menus.organizationSettings
    const href = isSite ? siteSettingsHref(org, siteSlug(site)) : organizationSettingsHref(org)
    const children = isSite
      ? siteSettingsSubPages(navigationSite, showReplays).map((page) => {
          const to = `/app/${org}/${siteSlug(site)}/settings/${page}`
          return subRow(`settings:site:${page}`, subPageLabel(page), to, pathname === to || pathname.startsWith(`${to}/`))
        })
      : settingsGroups({ showReplay: false, canManage: managers }, org)
          .flatMap((group) => group.items)
          .flatMap((item) => (item.kind === 'org' && item.value !== 'settings' ? [subRow(`settings:org:${item.value}`, item.label, item.to, pathname === item.to || pathname.startsWith(`${item.to}/`))] : []))
    return {
      key: `settings:${destination}`,
      label,
      href,
      active,
      children,
      chord: settingsChord,
      render: ({ label: text, id, kbd }) => (
        <SidebarMenuButton asChild className={rowClass} data-kbd={kbd || undefined} data-nav-item="" id={id} isActive={active} tooltip={tipFor(label, settingsChord)}>
          <Link
            aria-current={active ? 'page' : undefined}
            onClick={closeMobile}
            {...(isSite
              ? { params: { org, site: siteSlug(site) }, to: '/app/$org/$site/settings/general' as const }
              : { params: { org }, to: '/app/$org/settings' as const })}
          >
            {isSite ? <SettingsIcon /> : <Building2Icon />}
            <span className="truncate">{text}</span>
            <ChordHint chord={settingsChord} />
          </Link>
        </SidebarMenuButton>
      ),
    }
  })

  const allEntries = sections.flatMap((section) => section.entries)
  const pinnedRows = prefs.pinned.flatMap((key) => allEntries.filter((entry) => entry.key === key))
  const pinnedKeys = new Set(pinnedRows.map((entry) => entry.key))
  const activeSection = sections.find((section) => section.entries.some((entry) => entry.active && !pinnedKeys.has(entry.key)))?.key
  // `g` then a letter jumps to a page; the table is the rows that exist for this project and role.
  const goTo: Record<string, () => void> = {}
  for (const row of [...allEntries, ...settingsEntries]) if (row.chord) goTo[row.chord] ??= () => router.history.push(row.href)
  useGoTo(goTo)
  const activeSettings = settingsEntries.find((entry) => entry.active)?.key

  const baseGroups: MenuGroupData[] = [
    ...(pinnedRows.length > 0 ? [{ key: 'pinned', label: ui.sidebar.pinned, entries: pinnedRows }] : []),
    ...sections.map((section) => ({ ...section, entries: section.entries.filter((entry) => !pinnedKeys.has(entry.key)) })).filter((section) => section.entries.length > 0),
    { key: 'settings', label: ui.menus.settings, entries: settingsEntries, className: 'mt-auto' },
  ]
  const groups = filterGroups(baseGroups, query)
  const rows = useMemo(() => flattenRows(groups), [groups])
  // The cursor stays on its row while the results change, and falls back to the first result.
  const cursor = filtering ? (rows.some((row) => row.key === cursorKey) ? cursorKey : (rows[0]?.key ?? null)) : null

  // Every group starts open so the whole product is visible; a group someone collapses stays collapsed.
  const groupOpen = (key: string) => rail || filtering || (prefs.open[key] ?? true)

  // Landing on a page inside a collapsed group (palette, link, back button) opens that group.
  useEffect(() => {
    if (activeSection && prefs.open[activeSection] !== true && prefs.open[activeSection] !== undefined) setGroupOpen(activeSection, true)
    if (activeSettings && prefs.open[activeSettings] === false) setGroupOpen(activeSettings, true)
    // Only a change of the active group should reopen it, not the user collapsing it afterwards.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeSection, activeSettings, pathname])

  useEffect(() => {
    if (cursor) document.getElementById(optionId(cursor))?.scrollIntoView?.({ block: 'nearest' })
  }, [cursor])

  const focusFilter = () => {
    if (isMobile || document.querySelector('[role="dialog"]')) return
    if (rail) {
      focusAfterExpand.current = true
      setOpen(true)
    } else inputRef.current?.focus()
  }
  // The rail's filter button turns into the field when the sidebar peeks open; keep the keyboard focus on it.
  const filterButtonFocused = useRef(false)
  useEffect(() => {
    if (!rail && (focusAfterExpand.current || filterButtonFocused.current)) {
      focusAfterExpand.current = false
      filterButtonFocused.current = false
      inputRef.current?.focus()
    }
  }, [rail])
  // Own listener rather than useShortcuts: the shell registers that hook too, and `/` belongs to the sidebar.
  const focusFilterRef = useRef(focusFilter)
  focusFilterRef.current = focusFilter
  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key !== FILTER_SHORTCUT || event.ctrlKey || event.metaKey || event.altKey) return
      const target = event.target
      if (target instanceof HTMLElement && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))) return
      event.preventDefault()
      focusFilterRef.current()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])

  const clearFilter = () => {
    setQuery('')
    inputRef.current?.blur()
  }
  const onFilterKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      clearFilter()
      return
    }
    if (!filtering || rows.length === 0) return
    const at = Math.max(0, rows.findIndex((row) => row.key === cursor))
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const step = event.key === 'ArrowDown' ? 1 : -1
      setCursorKey(rows[(at + step + rows.length) % rows.length].key)
    } else if (event.key === 'Enter') {
      event.preventDefault()
      router.history.push(rows[at].href)
      closeMobile()
      clearFilter()
    }
  }
  const searchEverything = () => {
    closeMobile()
    if (onOpenPalette) onOpenPalette()
    else document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true, cancelable: true }))
  }

  const switcherTooltip = orgScope
    ? `${ui.sidebar.organizationScope}: ${membership.org.name}`
    : ui.sidebar.projectTooltip(site?.name ?? en.navigation.site, membership.org.name)

  return (
    <Sidebar aria-label={en.a11y2.applicationSidebar} collapsible="icon" role="complementary">
      <SidebarHeader className="sticky top-0 z-10 h-12 flex-row items-center border-b bg-sidebar p-0 px-2">
        <SidebarBrandLink onClick={closeMobile} />
      </SidebarHeader>
      <div className="flex flex-col gap-1.5 px-2 pt-2 pb-1" onFocusCapture={(event) => { filterButtonFocused.current = rail && event.target instanceof HTMLElement && event.target.hasAttribute('data-filter-trigger') }}>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton size="lg" className="h-11 gap-2 px-2 text-sm group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:p-0! md:h-9 data-[state=open]:bg-accent data-[state=open]:text-foreground hover:bg-accent/60 hover:text-foreground" tooltip={switcherTooltip}>
              {orgScope ? <Building2Icon aria-hidden="true" /> : site ? <SiteIcon iconUrl={site.icon_url} name={site.name} size={20} className="rounded-md" /> : null}
              {orgScope ? (
                <span className="flex min-w-0 flex-col text-left leading-4 group-data-[collapsible=icon]:hidden" data-scope="org">
                  <span className="truncate font-medium">{ui.sidebar.organizationScope}: {membership.org.name}</span>
                  {site ? <span className="truncate text-xs text-muted-foreground" data-testid="switcher-site-dimmed">{site.name}</span> : null}
                </span>
              ) : (
                <span className="flex min-w-0 flex-col text-left leading-4 group-data-[collapsible=icon]:hidden" data-scope="site">
                  <span className="truncate font-medium">{site?.name ?? en.navigation.site}</span>
                  <span className="truncate text-xs text-muted-foreground">{membership.org.name}</span>
                </span>
              )}
              <ChevronsUpDownIcon aria-hidden="true" className="ml-auto size-4 text-muted-foreground group-data-[collapsible=icon]:hidden" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-(--radix-dropdown-menu-trigger-width) min-w-64 max-w-80">
            <DropdownMenuLabel className={switcherLabelClass}>{en.navigation.projects}</DropdownMenuLabel>
            <DropdownMenuGroup className="max-h-72 overflow-y-auto">
              {membership.sites.map((item) => {
                const target = siteSwitchTarget(current, membership, item)
                return (
                  <DropdownMenuItem key={item.id} asChild>
                    <Link to={target.pathname as never} search={target.search as never} onClick={closeMobile}>
                      <SiteIcon iconUrl={item.icon_url} name={item.name} size={20} className="rounded-md" />
                      <span className="min-w-0 flex-1 truncate">{item.name}</span>
                      {item.is_demo ? <Badge variant="secondary">{ui.sidebar.demo}</Badge> : null}
                      {item.kind && item.kind !== 'web' ? <span className="text-xs text-muted-foreground">{ui.sidebar.kinds[item.kind]}</span> : null}
                      {!orgScope && item.id === site?.id ? <CurrentMark /> : null}
                    </Link>
                  </DropdownMenuItem>
                )
              })}
            </DropdownMenuGroup>
            {canCreateSites(membership.role) ? (
              <DropdownMenuGroup>
                <DropdownMenuItem asChild className={switcherActionClass}>
                  <Link to="/app/$org/sites/new" params={{ org: membership.org.slug }} onClick={closeMobile}>
                    <SwitcherGlyph dashed><PlusIcon className="size-3.5" /></SwitcherGlyph>
                    {en.navigation.addSite}
                  </Link>
                </DropdownMenuItem>
                {demoStateKnown && !hasDemo ? <AddDemoProjectItem membership={membership} /> : null}
              </DropdownMenuGroup>
            ) : null}
            <DropdownMenuSeparator />
            <OrganizationMenuGroup currentOrgId={membership.org.id} me={me} onSelect={closeMobile} />
          </DropdownMenuContent>
        </DropdownMenu>
        <SidebarFilterField
          activeId={cursor ? optionId(cursor) : undefined}
          count={rows.length}
          inputRef={inputRef}
          listId="sidebar-menu-results"
          onExpand={focusFilter}
          onKeyDown={onFilterKeyDown}
          onQueryChange={setQuery}
          query={query}
          rail={rail}
        />
        <p aria-live="polite" className="sr-only" role="status">{filtering ? ui.sidebar.matchCount(rows.length) : ''}</p>
      </div>
      <SidebarContent ref={nav.ref} className="gap-2 px-2 pt-1 group-data-[collapsible=icon]:overflow-auto" id="sidebar-menu-results" onFocus={nav.onFocus} onKeyDown={nav.onKeyDown}>
        {filtering && groups.length === 0 ? <NoMatches onSearchEverything={searchEverything} /> : null}
        {groups.map((group) => (
          <MenuGroup
            key={group.key}
            className={group.className}
            id={group.key}
            label={group.label}
            onToggle={() => setGroupOpen(group.key, !groupOpen(group.key))}
            open={groupOpen(group.key)}
          >
            {group.entries.map((entry) => {
              const subOpen = filtering || (prefs.open[entry.key] ?? (entry.active || Boolean(entry.children?.some((child) => child.active))))
              return (
                <MenuEntry
                  key={entry.key}
                  cursor={cursor}
                  entry={entry}
                  onToggle={() => setGroupOpen(entry.key, !subOpen)}
                  open={subOpen}
                  pin={entry.pinnable ? { pinned: pinnedKeys.has(entry.key), toggle: () => togglePin(entry.key) } : undefined}
                  query={query}
                />
              )
            })}
          </MenuGroup>
        ))}
      </SidebarContent>
      <SidebarAccountFooter me={me} />
    </Sidebar>
  )
}

function AddDemoProjectItem({ membership }: { membership: Membership }) {
  const createDemo = useCreateDemo({ id: membership.org.id, slug: membership.org.slug })
  return (
    <DropdownMenuItem
      className={switcherActionClass}
      disabled={createDemo.isPending}
      onSelect={() => createDemo.mutate()}
    >
      <SwitcherGlyph dashed><FlaskConicalIcon className="size-3.5" /></SwitcherGlyph>
      {createDemo.isPending ? ui.sidebar.addingDemo : ui.sidebar.addDemo}
    </DropdownMenuItem>
  )
}
