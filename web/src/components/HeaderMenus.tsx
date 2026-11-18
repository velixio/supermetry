import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { en } from '@/messages/en'
import { LogOutIcon, MoonIcon, ShieldCheckIcon, SunIcon, UserRoundIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem,
  DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { LanguageSubmenu } from '@/components/LanguageMenu'
import { useShellPrefs } from '@/components/ShellPrefsContext'
import { useManaged } from '@/lib/instanceConfig'
import { useTheme } from '@/lib/theme'
import { requestLogout } from '@/lib/session-lifecycle'
import { versionLabel } from '@/lib/version'
import type { Me } from '@/lib/me'
import { ui } from '@/messages/ui'

/**
 * Identity, plan, language, theme, account settings, instance administration and log out.
 * The sidebar footer opens it with its own trigger. The plan row is an empty, hidden slot
 * (`data-account-plan-slot`) that a hosting layer, such as Supermetry Cloud, can fill with its
 * plan name and link; core has no plans of its own.
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
  const own = useTheme()
  const { theme, toggleTheme } = useShellPrefs() ?? { theme: own.theme, toggleTheme: own.toggle }
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
        <div aria-label={ui.menus.plan} className="px-2 pb-1 text-sm empty:hidden" data-account-plan-slot="" role="group" />
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <LanguageSubmenu locale={me.preferences?.locale ?? ''} />
          <DropdownMenuItem onSelect={toggleTheme}>
            {theme === 'dark' ? <SunIcon aria-hidden="true" /> : <MoonIcon aria-hidden="true" />}
            {theme === 'dark' ? ui.menus.themeToLight : ui.menus.themeToDark}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem asChild><Link to="/app/account/profile"><UserRoundIcon aria-hidden="true" />{ui.menus.accountSettings}</Link></DropdownMenuItem>
          {me.user.instance_admin && !managed ? <DropdownMenuItem asChild><Link to="/app/admin/users"><ShieldCheckIcon aria-hidden="true" />{en.admin.link}</Link></DropdownMenuItem> : null}
          <DropdownMenuItem onSelect={() => void requestLogout()}><LogOutIcon aria-hidden="true" />{ui.menus.logOut}</DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
