import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

/**
 * The single h1 of a page, with an optional description and actions, in one compact row:
 * the description sits on the title's baseline and the actions to the right; on narrow
 * screens each part wraps under the previous one.
 */
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
    <header className={cn('flex flex-wrap items-center justify-between gap-x-4 gap-y-2', className)}>
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5">
        <h1 id={titleId} className="break-words text-lg leading-tight font-semibold tracking-tight">{title}</h1>
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
