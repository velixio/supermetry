import { Link } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

type EmptyStateProps = {
  trackingHref?: string
}

export function EmptyState({ trackingHref }: EmptyStateProps) {
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
