import { Button } from '@/components/ui/button'

type ErrorStateProps = {
  detail?: string
  onRetry?: () => void
  /** Page-level failures interrupt (alert); panel failures are announced politely so several do not compete. */
  level?: 'panel' | 'page'
}

export function ErrorState({ detail, onRetry, level = 'panel' }: ErrorStateProps) {
  return (
    <div className="flex min-h-36 flex-col items-start justify-center gap-3 px-4 py-6" role={level === 'page' ? 'alert' : 'status'}>
      <p className="text-sm text-muted-foreground">{detail ?? 'This data could not be loaded.'}</p>
      {onRetry ? <Button type="button" size="sm" variant="outline" onClick={onRetry}>Retry</Button> : null}
    </div>
  )
}
