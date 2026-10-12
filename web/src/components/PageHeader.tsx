import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

/** The single h1 of a page, with an optional description and actions. */
export function PageHeader({
  title,
  description,
  actions,
  className,
  titleId,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  className?: string
  /** Optional id for the h1, so a surrounding region can reference it with aria-labelledby. */
  titleId?: string
}) {
  return (
    <header className={cn('flex flex-wrap items-end justify-between gap-x-4 gap-y-3', className)}>
      <div className="flex min-w-0 flex-col gap-1">
        <h1 id={titleId} className="break-words text-xl font-semibold tracking-tight">{title}</h1>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? (
        <div className="flex flex-wrap items-center gap-2 empty:hidden" data-slot="page-header-actions">
          {actions}
        </div>
      ) : null}
    </header>
  )
}
