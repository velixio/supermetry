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
  inlineActions = false,
  titleHidden = false,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  className?: string
  /** Optional id for the h1, so a surrounding region can reference it with aria-labelledby. */
  titleId?: string
  /** Keep title and actions on one row below 640px; the actions shrink and truncate instead of wrapping. */
  inlineActions?: boolean
  /** The title is already on screen (breadcrumbs): keep the h1 for assistive tech only, so the page spends no height on it. */
  titleHidden?: boolean
}) {
  if (titleHidden && !actions && !description) return <h1 id={titleId} className="sr-only">{title}</h1>
  return (
    <header className={cn('flex min-h-8 flex-wrap items-center justify-between gap-x-4 gap-y-2', inlineActions && 'max-sm:flex-nowrap max-sm:gap-x-3', className)}>
      {/* One line: the title and, while it fits, its description; the full description stays available as a tooltip. */}
      <div className={cn('flex min-w-0 flex-1 items-baseline gap-x-3', inlineActions && 'max-sm:shrink-0')}>
        <h1 id={titleId} className="min-w-0 shrink-0 wrap-anywhere text-base leading-tight font-semibold tracking-tight">{title}</h1>
        {description ? <p className="min-w-0 truncate text-sm text-muted-foreground max-md:hidden" title={typeof description === 'string' ? description : undefined}>{description}</p> : null}
      </div>
      {actions ? (
        <div className={cn('flex flex-wrap items-center gap-2 empty:hidden', inlineActions && 'max-sm:min-w-0 max-sm:flex-1 max-sm:flex-nowrap max-sm:justify-end')} data-slot="page-header-actions">
          {actions}
        </div>
      ) : null}
    </header>
  )
}
