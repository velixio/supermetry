import { Link, useRouterState } from '@tanstack/react-router'
import { useEffect } from 'react'
import { Building2Icon, ShieldCheckIcon, UserRoundIcon } from 'lucide-react'

import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import {
  Sidebar,
  SidebarContent,
  SidebarHeader,
  SidebarMenuButton,
  SidebarMenuSubButton,
  useSidebar,
} from '@/components/ui/sidebar'
import { useSidebarPrefs } from '@/lib/shellPrefs'
import { useManaged } from '@/lib/instanceConfig'
import type { Me } from '@/lib/me'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

import { MenuEntry, MenuGroup, rowClass, subRowClass, type MenuRow } from './sidebar/menu'
import { OrganizationMenuGroup, SidebarAccountFooter, SidebarBrandLink } from './Sidebar'

export type AccountDestination = 'account' | 'admin'

/** Account settings for everyone; instance administration only for instance admins, and never on a managed instance. */
export function accountDestinations(me: Me, managed = false): AccountDestination[] {
  return me.user.instance_admin && !managed ? ['account', 'admin'] : ['account']
}

const accountSections = ['/app/account/profile', '/app/account/security', '/app/account/sessions', '/app/account/notifications']

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
  const { setOpenMobile, state: sidebarState, isMobile } = useSidebar()
  const managed = useManaged()
  const { prefs, setGroupOpen } = useSidebarPrefs(me.user.id)
  const closeMobile = () => setOpenMobile(false)
  const rail = sidebarState === 'collapsed' && !isMobile

  const entries: MenuRow[] = accountDestinations(me, managed).map((destination) => {
    const active = accountDestinationIsActive(destination, pathname)
    const label = destination === 'admin' ? en.admin.link : ui.menus.accountSettings
    const href = destination === 'admin' ? '/app/admin/users' : '/app/account/profile'
    const children: MenuRow[] = destination === 'admin' ? [] : accountSections.map((section) => {
      const name = section.split('/').pop() ?? ''
      const on = pathname === section || pathname.startsWith(`${section}/`)
      return {
        key: `account:${name}`,
        label: ui.crumbs.segments[name] ?? name,
        href: section,
        active: on,
        render: ({ label: text, id, kbd }) => (
          <SidebarMenuSubButton asChild className={subRowClass} data-kbd={kbd || undefined} id={id} isActive={on}>
            <Link aria-current={on ? 'page' : undefined} onClick={closeMobile} to={section as never}><span>{text}</span></Link>
          </SidebarMenuSubButton>
        ),
      }
    })
    return {
      key: `account:${destination}`,
      label,
      href,
      active,
      children,
      render: ({ label: text, id, kbd }) => (
        <SidebarMenuButton asChild className={rowClass} data-kbd={kbd || undefined} id={id} isActive={active} tooltip={label}>
          <Link aria-current={active ? 'page' : undefined} onClick={closeMobile} to={href}>
            {destination === 'admin' ? <ShieldCheckIcon /> : <UserRoundIcon />}
            <span>{text}</span>
          </Link>
        </SidebarMenuButton>
      ),
    }
  })
  const activeKey = entries.find((entry) => entry.active)?.key
  // Landing inside account settings opens its section list again if it was collapsed.
  useEffect(() => {
    if (activeKey && prefs.open[activeKey] === false) setGroupOpen(activeKey, true)
    // Only a change of route should reopen it, not the user collapsing it afterwards.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeKey, pathname])

  return (
    <Sidebar aria-label={en.a11y2.applicationSidebar} collapsible="icon" role="complementary">
      <SidebarHeader className="sticky top-0 z-10 h-12 flex-row items-center border-b bg-sidebar p-0 px-2">
        <SidebarBrandLink onClick={closeMobile} />
      </SidebarHeader>
      <div className="px-2 pt-2 pb-1">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton className="h-11 gap-2 px-2 text-sm font-medium md:h-9 hover:bg-accent/60 hover:text-foreground data-[state=open]:bg-accent data-[state=open]:text-foreground" tooltip={ui.sidebar.chooseOrganization}>
              <Building2Icon aria-hidden="true" />
              <span className="truncate">{ui.sidebar.chooseOrganization}</span>
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="min-w-56">
            <OrganizationMenuGroup me={me} onSelect={closeMobile} />
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <SidebarContent className="gap-2 px-2 pt-1 group-data-[collapsible=icon]:overflow-auto">
        <MenuGroup fixed id="account" label={ui.menus.account} onToggle={() => undefined} open rail={rail}>
          {entries.map((entry) => {
            const open = prefs.open[entry.key] ?? entry.active
            return <MenuEntry key={entry.key} cursor={null} entry={entry} onToggle={() => setGroupOpen(entry.key, !open)} open={open} query="" />
          })}
        </MenuGroup>
      </SidebarContent>
      <SidebarAccountFooter me={me} />
    </Sidebar>
  )
}
