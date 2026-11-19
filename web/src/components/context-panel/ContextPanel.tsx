import { useState, type ComponentType, type MouseEvent, type ReactNode } from 'react'
import { skipToken, useQuery } from '@tanstack/react-query'
import { useRouter, useRouterState } from '@tanstack/react-router'
import {
  BookmarkIcon, Building2Icon, ChevronRightIcon, CircleGaugeIcon, FlameIcon, FunnelIcon, GaugeIcon, Globe2Icon, HistoryIcon,
  LayoutDashboardIcon, Link2Icon, ListIcon, MousePointerClickIcon, PinIcon, RadioIcon, RepeatIcon, RouteIcon, SettingsIcon, StarIcon,
  TargetIcon, UserIcon, UsersIcon, VideoIcon, WalletIcon, ZapIcon,
} from 'lucide-react'

import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { dashboardKeys, type Dashboard } from '@/features/dashboards/api'
import { goalKeys } from '@/features/goals/api'
import { replayKeys } from '@/components/replay/api'
import type { Segment } from '@/features/segments/types'
import { segmentKeys } from '@/features/segments/api'
import { matchesSite } from '@/lib/matchSite'
import type { Membership } from '@/lib/me'
import { stringifySearch } from '@/lib/search'
import { readSidebarPrefs } from '@/lib/shellPrefs'
import { cn } from '@/lib/utils'
import { contextPanel as t } from '@/messages/contextPanel'
import { ui } from '@/messages/ui'

import { useContextPanel } from './ContextPanelProvider'
import { pagePath, parsePage, relatedLinks, type PageKey } from './relatedConfig'
import { recentsFor } from './recents'
import { recommend, type PanelFacts } from './recommend'
import { readCollapsedSections, writeCollapsedSections } from './storage'

const ICONS: Record<string, ComponentType<{ className?: string }>> = {
  dashboard: LayoutDashboardIcon, events: MousePointerClickIcon, sessions: ListIcon, realtime: RadioIcon, replays: VideoIcon,
  journeys: RouteIcon, retention: RepeatIcon, profiles: UserIcon, cohorts: UsersIcon, heatmaps: FlameIcon, vitals: GaugeIcon,
  funnels: FunnelIcon, goals: TargetIcon, revenue: WalletIcon, links: Link2Icon, sites: Globe2Icon, health: CircleGaugeIcon,
  'github-stars': StarIcon, overview: Building2Icon,
}

/** Last path segment of a page key, as a label. 'org/overview' reads as the organization settings page. */
export function pageLabel(key: PageKey): string {
  if (key === 'org/overview') return ui.crumbs.segments.settings
  const last = key.split('/').pop() ?? key
  return t.labels[last] ?? ui.sidebar.subPages[last] ?? ui.crumbs.segments[last] ?? last
}

const iconFor = (key: PageKey) => ICONS[key === 'org/overview' ? 'overview' : key.split('/').pop() ?? ''] ?? SettingsIcon

/** A sidebar pin key ("nav:/events", "settings:site:replay") as a page key. */
export function pinPageKey(pin: string): PageKey | undefined {
  if (pin.startsWith('nav:')) {
    const suffix = pin.slice(4).replace(/^\/+/, '')
    return suffix === '' ? 'dashboard' : suffix
  }
  const [, scope, page] = pin.split(':')
  if (pin.startsWith('settings:') && (scope === 'site' || scope === 'org')) {
    return scope === 'site' ? `settings/${page ?? 'general'}` : `org/${page ?? 'overview'}`
  }
  return undefined
}

const rowClass =
  'flex h-7 w-full min-w-0 items-center gap-2 rounded-md px-2 text-[13px] text-muted-foreground outline-hidden transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring/50 aria-[current=page]:bg-accent aria-[current=page]:text-accent-foreground max-md:h-11 [&_svg]:size-4 [&_svg]:shrink-0'

function PanelLink({ href, icon: Icon, children, current, onNavigate }: { href: string; icon: ComponentType<{ className?: string }>; children: ReactNode; current: boolean; onNavigate: () => void }) {
  const router = useRouter()
  const onClick = (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    router.history.push(href)
    onNavigate()
  }
  return (
    <a aria-current={current ? 'page' : undefined} className={rowClass} href={href} onClick={onClick}>
      <Icon aria-hidden="true" />
      <span className="truncate">{children}</span>
    </a>
  )
}

function Section({ id, title, collapsed, onToggle, children }: { id: string; title: string; collapsed: boolean; onToggle: () => void; children: ReactNode }) {
  const listId = `context-panel-${id}`
  return (
    <section aria-labelledby={`${listId}-title`} className="flex flex-col gap-0.5">
      <h3 className="m-0 text-xs font-medium text-muted-foreground" id={`${listId}-title`}>
        <button aria-controls={listId} aria-expanded={!collapsed} className="flex h-6 w-full items-center gap-1 rounded-md px-2 text-left outline-hidden hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 max-md:h-11" onClick={onToggle} type="button">
          <ChevronRightIcon aria-hidden="true" className={cn('size-3 shrink-0 transition-transform', !collapsed && 'rotate-90')} />
          {title}
        </button>
      </h3>
      <ul className="m-0 flex list-none flex-col gap-0.5 p-0" hidden={collapsed} id={listId}>{children}</ul>
    </section>
  )
}

/** Reads a query that some page already filled; never fetches. */
function useCached<T>(key: readonly unknown[], enabled = true): T | undefined {
  return useQuery<T>({ queryKey: key, queryFn: skipToken, enabled }).data
}

function usePanelFacts(orgId: number | undefined, siteId: number | undefined): PanelFacts {
  const status = useCached<{ install_status?: string }>(['tracking-status', siteId], siteId !== undefined)
  const goals = useCached<unknown[]>(goalKeys.all(siteId ?? 0), siteId !== undefined)
  const replay = useCached<{ enabled?: boolean; instance_enabled?: boolean }>(replayKeys.settings(siteId ?? 0), siteId !== undefined)
  const health = useCached<{ drops_503?: number }>(['data-health', orgId], orgId !== undefined)
  return {
    installStatus: status?.install_status,
    goalCount: Array.isArray(goals) ? goals.length : undefined,
    replayOff: replay ? replay.enabled === false && replay.instance_enabled !== false : undefined,
    rejectedEvents: health?.drops_503,
  }
}

type Item = { id: string; href: string; label: string; icon: ComponentType<{ className?: string }> }

export function ContextPanelBody({ membership, onNavigate }: { membership?: Membership; onNavigate: () => void }) {
  const { userId, recents } = useContextPanel()
  const { pathname, search } = useRouterState({ select: (state) => ({ pathname: state.location.pathname, search: state.location.search as Record<string, unknown> }) })
  const [collapsed, setCollapsed] = useState(() => readCollapsedSections(userId))
  const page = parsePage(pathname)
  const site = membership?.sites.find((item) => matchesSite(item, page.site))
  const facts = usePanelFacts(membership?.org.id, site?.id)
  const dashboards = useCached<Dashboard[]>(dashboardKeys.list(site?.id ?? 0), site !== undefined)
  const segments = useCached<Segment[]>(segmentKeys.site(site?.id ?? 0), site !== undefined)

  const toggleSection = (id: string) => {
    const next = collapsed.includes(id) ? collapsed.filter((item) => item !== id) : [...collapsed, id]
    setCollapsed(next)
    writeCollapsedSections(userId, next)
  }

  const quick: Item[] = []
  for (const pin of readSidebarPrefs(userId).pinned) {
    const key = pinPageKey(pin)
    const href = key && pagePath(key, page.org, page.site)
    if (key && href) quick.push({ id: pin, href, label: pageLabel(key), icon: PinIcon })
  }
  if (page.org && page.site) {
    for (const dashboard of (dashboards ?? []).filter((item) => item.id > 0).slice(0, 3)) {
      quick.push({ id: `dash:${dashboard.id}`, href: `/app/${page.org}/${page.site}?dash=${dashboard.id}`, label: t.savedDashboard(dashboard.name), icon: LayoutDashboardIcon })
    }
    for (const segment of (segments ?? []).slice(0, 3)) {
      quick.push({ id: `segment:${segment.id}`, href: `/app/${page.org}/${page.site}${stringifySearch({ f: segment.filters })}`, label: t.savedSegment(segment.name), icon: BookmarkIcon })
    }
  }
  const related: Item[] = relatedLinks(page, search).map((link) => ({ id: link.key, href: link.href, label: pageLabel(link.key), icon: iconFor(link.key) }))
  const suggested: Item[] = recommend(facts, { org: page.org, site: page.site, pathname }).map((item) => ({ id: item.id, href: item.href, label: t.recommend[item.id], icon: ZapIcon }))
  const recent: Item[] = recentsFor(recents, page.org, page.site, pathname).map((item) => ({ id: item.path, href: item.href, label: item.label, icon: HistoryIcon }))

  const sections: { id: string; title: string; items: Item[] }[] = [
    { id: 'quick', title: t.sections.quick, items: quick },
    { id: 'related', title: t.sections.related, items: related },
    { id: 'recommended', title: t.sections.recommended, items: suggested },
    { id: 'recent', title: t.sections.recent, items: recent },
  ]
  return (
    <div className="flex flex-col gap-3 p-2">
      {sections.filter((section) => section.items.length > 0).map((section) => (
        <Section collapsed={collapsed.includes(section.id)} id={section.id} key={section.id} onToggle={() => toggleSection(section.id)} title={section.title}>
          {section.items.map((item) => (
            <li key={item.id}>
              <PanelLink current={item.href.split('?')[0] === pathname} href={item.href} icon={item.icon} onNavigate={onNavigate}>{item.label}</PanelLink>
            </li>
          ))}
        </Section>
      ))}
    </div>
  )
}

/**
 * The panel itself: a 280px column right of <main> on wide screens, a right Sheet below.
 * It is a complementary landmark, after <main> in the tab order.
 */
export function ContextPanel({ membership }: { membership?: Membership }) {
  const { open, wide, panelId, close, toggleRef } = useContextPanel()
  if (!open) return null
  if (wide) {
    return (
      <aside aria-label={t.label} className="sticky top-12 h-[calc(100svh-3rem)] w-[280px] shrink-0 self-start overflow-y-auto bg-sidebar" id={panelId}>
        <ContextPanelBody membership={membership} onNavigate={() => undefined} />
      </aside>
    )
  }
  return (
    <Sheet onOpenChange={(next) => { if (!next) close() }} open>
      <SheetContent
        className="w-[280px] gap-0 overflow-y-auto p-0 sm:max-w-[280px]"
        id={panelId}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          toggleRef.current?.focus()
        }}
      >
        <SheetTitle className="sr-only">{t.label}</SheetTitle>
        <SheetDescription className="sr-only">{t.label}</SheetDescription>
        <div className="pt-10">
          <ContextPanelBody membership={membership} onNavigate={close} />
        </div>
      </SheetContent>
    </Sheet>
  )
}
