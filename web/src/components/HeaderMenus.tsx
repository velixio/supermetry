import { Link } from '@tanstack/react-router'
import { en } from '@/messages/en'
import { Building2Icon, LogOutIcon, SettingsIcon, ShieldCheckIcon, UserRoundIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem,
  DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { requestLogout } from '@/lib/session-lifecycle'
import type { Me, Membership, Site } from '@/lib/me'
import { siteSlug } from './Sidebar'
import { ui } from '@/messages/ui'

export function HeaderMenus({ me, membership, site }: { me: Me; membership?: Membership; site?: Site }) {
  const organizations = membership ? [membership] : me.memberships
  return (
    <div className="flex shrink-0 items-center gap-1">
      {organizations.length > 0 ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="sm"><SettingsIcon aria-hidden="true" />{ui.menus.settings}</Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72 max-w-[calc(100vw-2rem)]" aria-label={ui.menus.settingsMenu}>
            <DropdownMenuGroup>
              {organizations.map((item) => (
                <DropdownMenuItem asChild key={item.org.id}>
                  <Link to="/app/$org/settings" params={{ org: item.org.slug }}>
                    <Building2Icon aria-hidden="true" />
                    <span className="min-w-0"><span className="block">{ui.menus.organizationSettings}</span><span className="block truncate text-xs text-muted-foreground">{item.org.name}</span></span>
                  </Link>
                </DropdownMenuItem>
              ))}
              {membership && site && membership.role !== 'viewer' ? (
                <DropdownMenuItem asChild>
                  <Link to="/app/$org/$site/settings/general" params={{ org: membership.org.slug, site: siteSlug(site) }}>
                    <SettingsIcon aria-hidden="true" />
                    <span className="min-w-0"><span className="block">{ui.menus.siteSettings}</span><span className="block truncate text-xs text-muted-foreground">{site.name}</span></span>
                  </Link>
                </DropdownMenuItem>
              ) : null}
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="sm"><UserRoundIcon aria-hidden="true" />{ui.menus.account}</Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-72 max-w-[calc(100vw-2rem)]" aria-label={ui.menus.accountMenu}>
          <DropdownMenuLabel className="flex flex-col gap-1">
            <span className="truncate">{me.user.name || ui.menus.yourAccount}</span>
            <span className="break-all text-xs font-normal text-muted-foreground">{me.user.email}</span>
            <span className="text-xs font-normal text-muted-foreground">Supermetry {me.version || 'dev'}</span>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem asChild><Link to="/app/account/profile"><UserRoundIcon aria-hidden="true" />{ui.menus.accountSettings}</Link></DropdownMenuItem>
            {me.user.instance_admin ? <DropdownMenuItem asChild><Link to="/app/admin/users"><ShieldCheckIcon aria-hidden="true" />{en.admin.link}</Link></DropdownMenuItem> : null}
            <DropdownMenuItem onSelect={() => void requestLogout()}><LogOutIcon aria-hidden="true" />{ui.menus.logOut}</DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
