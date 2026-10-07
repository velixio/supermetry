import { Link } from '@tanstack/react-router'

import { Button } from '@/components/ui/button'

type EmptyStateProps = {
  trackingHref?: string
}

export function EmptyState({ trackingHref }: EmptyStateProps) {
  return (
    <div className="flex min-h-36 flex-col items-center justify-center gap-3 px-4 py-6 text-center">
      <p className="text-sm text-muted-foreground">No data for this date range.</p>
      {trackingHref ? (
        <Button asChild size="sm" variant="outline">
          <Link to={trackingHref}>Set up tracking</Link>
        </Button>
      ) : null}
    </div>
  )
}
