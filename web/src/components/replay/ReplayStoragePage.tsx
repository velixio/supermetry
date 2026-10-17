import { toast } from 'sonner'

import { ErrorState } from '@/components/ErrorState'
import { Skeleton } from '@/components/ui/skeleton'
import { ReplayApiError } from '@/lib/replay-api'

import { useReplayStorage, useSaveReplayStorage } from './api'
import { ReplayStorageForm } from './ReplayStorageForm'

export function ReplayStoragePage() {
  const storage = useReplayStorage()
  const save = useSaveReplayStorage()
  if (storage.isPending) return <Skeleton className="h-96 w-full" />
  if (storage.isError) return storage.error instanceof ReplayApiError && storage.error.status === 404
    ? null
    : <ErrorState level="page" detail={storage.error.message} onRetry={() => void storage.refetch()} />
  return (
    <ReplayStorageForm
      saving={save.isPending}
      settings={storage.data}
      onSave={(settings, migrate) => save.mutate({ settings, migrate }, {
        onSuccess: () => toast.success(migrate ? 'Storage migration started' : 'Replay storage saved'),
        onError: (error) => toast.error(error.message),
      })}
    />
  )
}
