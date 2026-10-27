import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { en } from '@/messages/en'
import { LogOutIcon, ShieldCheckIcon, UserRoundIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem,
  DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useManaged } from '@/lib/instanceConfig'
import { requestLogout } from '@/lib/session-lifecycle'
import { versionLabel } from '@/lib/version'
import type { Me } from '@/lib/me'
import { ui } from '@/messages/ui'

/**
 * Identity, build version, account settings, instance administration and log out.
 * The sidebar footer opens it with its own trigger.
 */
export function AccountMenu({
  me,
  trigger,
  side,
  align = 'end',
}: {
  me: Me
  trigger?: ReactNode
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
}) {
  const managed = useManaged()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {trigger ?? <Button variant="ghost" size="sm"><UserRoundIcon aria-hidden="true" />{ui.menus.account}</Button>}
      </DropdownMenuTrigger>
      <DropdownMenuContent side={side} align={align} className="w-72 max-w-[calc(100vw-2rem)]" aria-label={ui.menus.accountMenu} aria-labelledby={undefined}>
        <DropdownMenuLabel className="flex flex-col gap-1">
          <span className="truncate">{me.user.name || ui.menus.yourAccount}</span>
          <span className="break-all text-xs font-normal text-muted-foreground">{me.user.email}</span>
          <span className="text-xs font-normal text-muted-foreground">Supermetry {versionLabel(me.version)}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuItem asChild><Link to="/app/account/profile"><UserRoundIcon aria-hidden="true" />{ui.menus.accountSettings}</Link></DropdownMenuItem>
          {me.user.instance_admin && !managed ? <DropdownMenuItem asChild><Link to="/app/admin/users"><ShieldCheckIcon aria-hidden="true" />{en.admin.link}</Link></DropdownMenuItem> : null}
          <DropdownMenuItem onSelect={() => void requestLogout()}><LogOutIcon aria-hidden="true" />{ui.menus.logOut}</DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
