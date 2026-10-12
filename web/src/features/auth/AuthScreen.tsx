import type { ReactNode } from 'react'

import { BrandLogo } from '@/components/BrandLogo'
import { PoweredBySupermetry } from '@/components/PoweredBySupermetry'

export function AuthScreen({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return (
    <main className="dark grid min-h-dvh place-items-center bg-background px-5 py-12 text-foreground">
      <section className="flex w-full max-w-sm flex-col gap-8">
        <header className="flex flex-col gap-2 border-l-2 border-accent pl-5">
          <BrandLogo className="h-7 w-auto self-start" />
          <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
          <p className="text-sm leading-6 text-muted-foreground">{description}</p>
        </header>
        {children}
        <PoweredBySupermetry className="text-xs text-muted-foreground hover:text-foreground" />
      </section>
    </main>
  )
}
