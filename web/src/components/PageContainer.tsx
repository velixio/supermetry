import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

const widths = {
  wide: 'max-w-screen-2xl',
  narrow: 'max-w-5xl',
} as const

/** Width and vertical rhythm for a page. Padding stays on `<main>` in AppShell. */
export function PageContainer({
  width = 'wide',
  className,
  children,
}: {
  width?: keyof typeof widths
  className?: string
  children: ReactNode
}) {
  return <div className={cn('mx-auto flex w-full min-w-0 flex-col gap-4', widths[width], className)}>{children}</div>
}
