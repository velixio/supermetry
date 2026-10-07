import {
  ActivityIcon,
  ChartNoAxesCombinedIcon,
  CircleGaugeIcon,
  FilterIcon,
  GoalIcon,
  Globe2Icon,
  PlusIcon,
  SettingsIcon,
} from 'lucide-react'

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
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from '@/components/ui/sidebar'
import type { Me, Membership, Site } from '@/routes/$org'
import { en } from '@/messages/en'

export type Role = 'owner' | 'admin' | 'editor' | 'viewer'

const coreNavigation = [
  { label: en.navigation.dashboard, suffix: '', icon: ChartNoAxesCombinedIcon, scope: 'site' },
  { label: en.navigation.funnels, suffix: '/funnels', icon: FilterIcon, scope: 'site' },
  { label: en.navigation.realtime, suffix: '/realtime', icon: ActivityIcon, scope: 'site' },
  { label: en.navigation.goals, suffix: '/goals', icon: GoalIcon, scope: 'site' },
] as const

const managementNavigation = [
  { label: en.navigation.settings, suffix: '/settings/general', icon: SettingsIcon, scope: 'site' },
  { label: en.navigation.sites, suffix: '/sites', icon: Globe2Icon, scope: 'org' },
  { label: en.navigation.health, suffix: '/health', icon: CircleGaugeIcon, scope: 'org' },
] as const

export type NavigationItem =
  | (typeof coreNavigation)[number]
  | (typeof managementNavigation)[number]

export function visibleNavigation(role: Role, hasSite = true): NavigationItem[] {
  const items: NavigationItem[] = role === 'viewer'
    ? [...coreNavigation, managementNavigation[1]]
    : role === 'editor'
      ? [...coreNavigation, managementNavigation[0], managementNavigation[1]]
      : [...coreNavigation, ...managementNavigation]
  return hasSite ? items : items.filter((item) => item.scope === 'org')
}

export function canCreateSites(role: Role): boolean {
  return role === 'owner' || role === 'admin'
}

function siteSlug(site: Site): string {
  return site.slug ?? site.public_id ?? String(site.id)
}

export function organizationHref(membership: Membership): string {
  const site = membership.sites[0]
  return site
    ? `/${membership.org.slug}/${siteSlug(site)}`
    : `/${membership.org.slug}`
}

export function navigationHref(
  item: NavigationItem,
  org: string,
  site: string,
): string {
  const base = item.scope === 'org' ? `/${org}` : `/${org}/${site}`
  return `${base}${item.suffix}`
}

function activeSite(membership: Membership, requested?: string): Site | undefined {
  return membership.sites.find((site) => siteSlug(site) === requested) ?? membership.sites[0]
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
                  <a href={organizationHref(item)}>{item.org.name}</a>
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton tooltip={en.navigation.site}>
              <span className="truncate">{site?.name ?? en.navigation.site}</span>
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuLabel>{en.navigation.site}</DropdownMenuLabel>
            <DropdownMenuGroup>
              {membership.sites.map((item) => (
                <DropdownMenuItem key={item.id} asChild>
                  <a href={`/${membership.org.slug}/${siteSlug(item)}`}>{item.name}</a>
                </DropdownMenuItem>
              ))}
              {canCreateSites(membership.role) ? (
                <DropdownMenuItem asChild>
                  <a href={`/${membership.org.slug}/sites/new`}>
                    <PlusIcon />
                    {en.navigation.addSite}
                  </a>
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
              {visibleNavigation(membership.role, Boolean(site)).map((item) => (
                <SidebarMenuItem key={item.label}>
                  <SidebarMenuButton asChild tooltip={item.label}>
                    <a
                      href={navigationHref(
                        item,
                        membership.org.slug,
                        currentSite,
                      )}
                    >
                      <item.icon />
                      <span>{item.label}</span>
                    </a>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <span className="truncate px-2 text-xs text-muted-foreground">
          {me.user.email}
        </span>
      </SidebarFooter>
    </Sidebar>
  )
}
