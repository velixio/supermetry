import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { MoonIcon, SunIcon } from 'lucide-react'

import { HeaderMenus } from './HeaderMenus'
import { BrandLogo } from './BrandLogo'
import { PoweredBySupermetry } from './PoweredBySupermetry'
import { Button } from './ui/button'
import { Toaster } from './ui/sonner'
import { useTheme } from '@/lib/theme'
import type { Me } from '@/routes/app.$org'

export function AccountFrame({ me, children }: { me: Me; children: ReactNode }) {
  const { theme, toggle } = useTheme()
  return (
    <div className="min-h-dvh bg-background text-foreground">
      <header className="sticky top-0 z-20 flex h-14 items-center justify-between gap-2 border-b bg-background px-4">
        <Link aria-label="Supermetry" to="/app"><BrandLogo className="h-6 w-auto" /></Link>
        <div className="flex items-center gap-1">
          <Button size="icon" variant="ghost" aria-label={theme === 'dark' ? 'Use light theme' : 'Use dark theme'} onClick={toggle}>
            {theme === 'dark' ? <SunIcon /> : <MoonIcon />}
          </Button>
          <HeaderMenus me={me} />
        </div>
      </header>
      <main className="mx-auto flex max-w-5xl flex-col gap-6 p-4 md:p-6">{children}</main>
      <footer className="mx-auto w-full max-w-5xl px-4 pb-6 text-xs text-muted-foreground md:px-6">
        <PoweredBySupermetry className="hover:text-foreground" />
      </footer>
      <Toaster />
    </div>
  )
}
