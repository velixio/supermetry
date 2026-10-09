import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { MoonIcon, SunIcon } from 'lucide-react'

import { HeaderMenus } from './HeaderMenus'
import { Button } from './ui/button'
import { Toaster } from './ui/sonner'
import { useTheme } from '@/lib/theme'
import type { Me } from '@/routes/$org'

export function AccountFrame({ me, children }: { me: Me; children: ReactNode }) {
  const { theme, toggle } = useTheme()
  return (
    <div className="min-h-dvh bg-background text-foreground">
      <header className="sticky top-0 z-20 flex h-14 items-center justify-between gap-2 border-b bg-background px-4">
        <Link className="font-semibold" to="/">Panel</Link>
        <div className="flex items-center gap-1">
          <Button size="icon" variant="ghost" aria-label={theme === 'dark' ? 'Use light theme' : 'Use dark theme'} onClick={toggle}>
            {theme === 'dark' ? <SunIcon /> : <MoonIcon />}
          </Button>
          <HeaderMenus me={me} />
        </div>
      </header>
      <main className="mx-auto flex max-w-5xl flex-col gap-6 p-4 md:p-6">{children}</main>
      <Toaster />
    </div>
  )
}
