import type { ReactNode } from 'react'

import { BrandLogo } from '@/components/BrandLogo'
import { PoweredBySupermetry } from '@/components/PoweredBySupermetry'
import { SupportLink } from '@/components/SupportLink'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'

export function AuthScreen({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return (
    <main className="dark relative isolate grid min-h-dvh place-items-center overflow-hidden bg-background px-5 py-12 text-foreground">
      <section className="flex w-full max-w-sm flex-col items-center gap-6">
        <BrandLogo className="h-8 w-auto" />
        <Card className="w-full shadow-sm">
          <CardHeader className="px-6">
            <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
            <CardDescription>{description}</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4 px-6">{children}</CardContent>
        </Card>
        <SupportLink className="text-xs" />
        <PoweredBySupermetry className="text-xs text-muted-foreground hover:text-foreground" />
      </section>
    </main>
  )
}
