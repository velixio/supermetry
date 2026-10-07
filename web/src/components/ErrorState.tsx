import { Button } from '@/components/ui/button'

type ErrorStateProps = {
  detail?: string
  onRetry: () => void
}

export function ErrorState({ detail, onRetry }: ErrorStateProps) {
  return (
    <div className="flex min-h-36 flex-col items-start justify-center gap-3 px-4 py-6" role="alert">
      <p className="text-sm text-muted-foreground">{detail ?? 'This data could not be loaded.'}</p>
      <Button type="button" size="sm" variant="outline" onClick={onRetry}>
        Retry
      </Button>
    </div>
  )
}
