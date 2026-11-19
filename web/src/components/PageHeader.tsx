import type { ReactNode } from 'react'

import { InfoTip } from '@/components/InfoTip'
import { TooltipProvider } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

import { HeaderToolbar, useHeaderToolbar } from './HeaderToolbar'

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
  const toolbar = useHeaderToolbar()
  // A title that only repeats (or extends, like "User journeys") the last breadcrumb, or is already on screen,
  // stays for assistive tech only, so the page starts with its content.
  const repeatsCrumb = titleHidden || (typeof title === 'string' && toolbar.pageLabel !== undefined && title.toLowerCase().includes(toolbar.pageLabel.toLowerCase()))
  if (!toolbar.active && toolbar.node && repeatsCrumb) {
    // Narrow screens inside the shell: the header bar has no room, so the actions keep one swipeable row.
    return (
      <>
        <h1 id={titleId} className="sr-only">{title}</h1>
        {description ? <p className="sr-only">{description}</p> : null}
        {actions ? (
          <div className={cn('relative -mx-3 flex min-w-0 flex-nowrap items-center gap-2 overflow-x-auto px-3 py-0.5 [scrollbar-width:none] empty:hidden *:shrink-0 [&_.flex-wrap]:flex-nowrap', className)} data-slot="page-header-actions">
            {actions}
          </div>
        ) : null}
      </>
    )
  }
  if (toolbar.active) {
    // App-shell layout: the actions join the header bar.
    const portalled = actions ? <HeaderToolbar>{actions}</HeaderToolbar> : null
    if (repeatsCrumb) {
      return (
        <>
          <h1 id={titleId} className="sr-only">{title}</h1>
          {description ? <p className="sr-only">{description}</p> : null}
          {portalled}
        </>
      )
    }
    return (
      <>
        <InlinePageHeader className={className} description={description} title={title} titleId={titleId} />
        {portalled}
      </>
    )
  }
  if (titleHidden && !actions && !description) return <h1 id={titleId} className="sr-only">{title}</h1>
  return <InlinePageHeader actions={actions} className={className} description={description} inlineActions={inlineActions} title={title} titleId={titleId} />
}

function InlinePageHeader({ title, description, actions, className, titleId, inlineActions = false }: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  className?: string
  titleId?: string
  inlineActions?: boolean
}) {
  return (
    <header className={cn('flex min-h-8 flex-wrap items-center justify-between gap-x-4 gap-y-2', inlineActions && 'max-sm:flex-nowrap max-sm:gap-x-3', className)}>
      {/* The title, with its description one hover, focus or tap away in an InfoTip (D104). */}
      <div className={cn('flex min-w-0 flex-1 items-center gap-x-2', inlineActions && 'max-sm:shrink-0')}>
        <h1 id={titleId} className="min-w-0 shrink-0 wrap-anywhere text-base leading-tight font-semibold tracking-tight">{title}</h1>
        {typeof description === 'string' && description ? <TooltipProvider><InfoTip label={typeof title === 'string' ? `About ${title}` : undefined}>{description}</InfoTip></TooltipProvider> : null}
        {description && typeof description !== 'string' ? <div className="min-w-0 truncate text-sm text-muted-foreground max-md:hidden">{description}</div> : null}
      </div>
      {actions ? (
        <div className={cn('flex max-w-full min-w-0 flex-wrap items-center gap-2 empty:hidden', inlineActions && 'max-sm:min-w-0 max-sm:flex-1 max-sm:flex-nowrap max-sm:justify-end')} data-slot="page-header-actions">
          {actions}
        </div>
      ) : null}
    </header>
  )
}
