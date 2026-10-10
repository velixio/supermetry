import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import { useCreateDemo, useDismissDemoPrompt, type DemoOrganization } from './useDemo'

export function DemoChoice({
  org,
  onDismissed,
}: {
  org: DemoOrganization
  onDismissed: () => void
}) {
  const create = useCreateDemo(org)
  const dismiss = useDismissDemoPrompt()
  const error = create.error ?? dismiss.error

  return (
    <Alert>
      <AlertTitle>Explore with demo data</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-3">
        <p>Open a ready-to-use analytics project while you set up your own site.</p>
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            disabled={create.isPending || dismiss.isPending}
            onClick={() => create.mutate()}
          >
            {create.isPending ? 'Creating demo project…' : 'Explore with demo data'}
          </Button>
          <Button
            type="button"
            variant="ghost"
            disabled={create.isPending || dismiss.isPending}
            onClick={() => dismiss.mutate(undefined, { onSuccess: onDismissed })}
          >
            Skip for now
          </Button>
        </div>
        {error ? <p role="alert">{error instanceof Error ? error.message : 'The request failed.'}</p> : null}
      </AlertDescription>
    </Alert>
  )
}
