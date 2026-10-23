import { Link, useRouterState } from '@tanstack/react-router'
import { Building2Icon, ShieldCheckIcon, UserRoundIcon } from 'lucide-react'

import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'
import type { Me } from '@/lib/me'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

import { GitHubStarsNav, OrganizationMenuGroup, SidebarAccountFooter, SidebarBrandLink } from './Sidebar'

export type AccountDestination = 'account' | 'admin'

/** Account settings for everyone; instance administration only for instance admins. */
export function accountDestinations(me: Me): AccountDestination[] {
  return me.user.instance_admin ? ['account', 'admin'] : ['account']
}

const accountSections = ['/app/account/profile', '/app/account/security', '/app/account/sessions']

export function accountDestinationIsActive(destination: AccountDestination, pathname: string): boolean {
  const within = (base: string) => pathname === base || pathname.startsWith(`${base}/`)
  return destination === 'admin' ? within('/app/admin') : accountSections.some(within)
}

/**
 * The sidebar outside an organization: the sites overview, account and admin pages,
 * and an organization the user cannot open. It has no site switcher and no analytics navigation.
 */
export function AccountSidebar({ me }: { me: Me }) {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const { setOpenMobile } = useSidebar()
  const closeMobile = () => setOpenMobile(false)

  return (
    <Sidebar aria-label={en.a11y2.applicationSidebar} collapsible="icon" role="complementary">
      <SidebarHeader>
        <SidebarBrandLink onClick={closeMobile} />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton className="h-auto py-1.5 group-data-[collapsible=icon]:p-1.5!" tooltip={ui.sidebar.chooseOrganization}>
              <Building2Icon aria-hidden="true" />
              <span className="truncate">{ui.sidebar.chooseOrganization}</span>
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="min-w-56">
            <OrganizationMenuGroup me={me} onSelect={closeMobile} />
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarHeader>
      <SidebarContent className="group-data-[collapsible=icon]:overflow-auto">
        <SidebarGroup>
          <SidebarGroupLabel asChild><h2 id="sidebar-account-heading">{ui.menus.account}</h2></SidebarGroupLabel>
          <SidebarGroupContent>
            <nav aria-labelledby="sidebar-account-heading">
              <SidebarMenu>
                {accountDestinations(me).map((destination) => {
                  const active = accountDestinationIsActive(destination, pathname)
                  const label = destination === 'admin' ? en.admin.link : ui.menus.accountSettings
                  return (
                    <SidebarMenuItem key={destination}>
                      <SidebarMenuButton asChild className="h-11 md:h-8" isActive={active} tooltip={label}>
                        <Link
                          aria-current={active ? 'page' : undefined}
                          onClick={closeMobile}
                          to={destination === 'admin' ? '/app/admin/users' : '/app/account/profile'}
                        >
                          {destination === 'admin' ? <ShieldCheckIcon /> : <UserRoundIcon />}
                          <span>{label}</span>
                        </Link>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  )
                })}
              </SidebarMenu>
            </nav>
          </SidebarGroupContent>
        </SidebarGroup>
        <GitHubStarsNav me={me} onNavigate={closeMobile} />
      </SidebarContent>
      <SidebarAccountFooter me={me} />
    </Sidebar>
  )
}
