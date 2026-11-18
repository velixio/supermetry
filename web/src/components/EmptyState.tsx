import type { ComponentType, SVGProps } from 'react'
import { Link } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'
import { uiOnboarding } from '@/messages/uiOnboarding'

type EmptyStateProps = {
  /** Legacy card form: "No data for this date range." with an optional tracking link. */
  trackingHref?: string
  /** Page form: say what the page is for and offer one next step. */
  title?: string
  description?: string
  icon?: ComponentType<SVGProps<SVGSVGElement>>
  /** The one primary action: a link when `href` is set, otherwise a button. */
  action?: { label: string; href?: string; onClick?: () => void }
  docsHref?: string
}

export function EmptyState({ trackingHref, title, description, icon: Icon, action, docsHref }: EmptyStateProps) {
  if (title) {
    return (
      <section aria-labelledby="empty-state-title" className="flex min-h-48 flex-col items-center justify-center gap-3 rounded-xl border border-dashed bg-card px-4 py-10 text-center">
        {Icon ? <Icon aria-hidden="true" className="size-8 text-muted-foreground" /> : null}
        <h2 id="empty-state-title" className="text-base font-semibold">{title}</h2>
        {description ? <p className="max-w-md text-sm text-muted-foreground">{description}</p> : null}
        {action ? (
          action.href ? (
            <Button asChild size="sm"><Link to={action.href}>{action.label}</Link></Button>
          ) : (
            <Button size="sm" type="button" onClick={action.onClick}>{action.label}</Button>
          )
        ) : null}
        {docsHref ? (
          <a className="text-sm font-medium text-foreground underline underline-offset-4" href={docsHref} rel="noreferrer" target="_blank">
            {uiOnboarding.empty.docs}
          </a>
        ) : null}
      </section>
    )
  }
  return (
    <div className="flex min-h-36 flex-col items-center justify-center gap-3 px-4 py-6 text-center">
      <p className="text-sm text-muted-foreground">{en.polish.emptyRange}</p>
      <p className="text-sm text-muted-foreground">{en.polish.emptyNext}</p>
      {trackingHref ? (
        <Button asChild size="sm" variant="outline">
          <Link to={trackingHref}>{ui.emptyState.setUpTracking}</Link>
        </Button>
      ) : null}
    </div>
  )
}
