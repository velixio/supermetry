import { toast } from 'sonner'

import { ErrorState } from '@/components/ErrorState'
import { Skeleton } from '@/components/ui/skeleton'
import { ReplayApiError } from '@/lib/replay-api'

import { useReplaySettings, useSaveReplaySettings } from './api'
import { ReplaySettingsForm } from './ReplaySettingsForm'

export function ReplaySettingsPage({ siteId, editable }: { siteId: number; editable: boolean }) {
  const settings = useReplaySettings(siteId)
  const save = useSaveReplaySettings(siteId)
  if (settings.isPending) return <Skeleton className="h-96 w-full" />
  if (settings.isError) return settings.error instanceof ReplayApiError && settings.error.status === 404
    ? null
    : <ErrorState detail={settings.error.message} onRetry={() => void settings.refetch()} />
  return (
    <ReplaySettingsForm
      editable={editable}
      saving={save.isPending}
      settings={settings.data}
      onSave={(value) => save.mutate(value, {
        onSuccess: () => toast.success('Replay settings saved'),
        onError: (error) => toast.error(error.message),
      })}
    />
  )
}
