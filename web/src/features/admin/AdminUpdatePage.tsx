import { ExternalLinkIcon, InfoIcon } from 'lucide-react'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { en } from '@/messages/en'
import { AdminPage, formatDate } from './AdminCommon'
import { useUpdateStatus } from './adminApi'

function VersionValue({ value }: { value: string | null | undefined }) {
  return <dd className="break-all text-sm font-medium">{value || en.admin.unknownVersion}</dd>
}

export function AdminUpdatePage() {
  const status = useUpdateStatus(true)
  const errors = [status.error instanceof Error ? status.error.message : status.error, status.data?.check_error]
    .filter((error): error is string => typeof error === 'string' && error.length > 0)

  return (
    <AdminPage title={en.admin.update} description={en.admin.updateDescription}>
      {errors.length > 0 ? (
        <Alert variant="destructive">
          <AlertTitle>{en.admin.updateCheckFailed}</AlertTitle>
          <AlertDescription>{errors.join(' ')}</AlertDescription>
        </Alert>
      ) : null}

      {status.isPending ? (
        <div role="status" aria-label={en.admin.loading} className="flex max-w-xl flex-col gap-3">
          <Skeleton className="h-32 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      ) : null}

      {status.data ? (
        <Card className="max-w-xl">
          <CardHeader>
            <CardTitle>{en.admin.versionStatus}</CardTitle>
            <CardDescription>
              {status.data.check_error
                ? en.admin.updateUnknown
                : status.data.update_available
                  ? en.admin.newRelease
                  : en.admin.upToDate}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <dl className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
              <div className="flex min-w-0 flex-col gap-1">
                <dt className="text-sm text-muted-foreground">{en.admin.currentVersion}</dt>
                <VersionValue value={status.data.current_version} />
              </div>
              <div className="flex min-w-0 flex-col gap-1">
                <dt className="text-sm text-muted-foreground">{en.admin.latestVersion}</dt>
                <VersionValue value={status.data.latest_version} />
              </div>
              <div className="flex min-w-0 flex-col gap-1">
                <dt className="text-sm text-muted-foreground">{en.admin.published}</dt>
                <VersionValue value={status.data.published_at ? formatDate(status.data.published_at) : null} />
              </div>
              <div className="flex min-w-0 flex-col gap-1">
                <dt className="text-sm text-muted-foreground">{en.admin.checked}</dt>
                <VersionValue value={status.data.checked_at ? formatDate(status.data.checked_at) : null} />
              </div>
            </dl>
          </CardContent>
          {status.data.release_url ? (
            <CardFooter>
              <Button asChild variant="outline">
                <a href={status.data.release_url} target="_blank" rel="noreferrer">
                  {en.admin.viewRelease}
                  <ExternalLinkIcon data-icon="inline-end" aria-hidden="true" />
                </a>
              </Button>
            </CardFooter>
          ) : null}
        </Card>
      ) : null}

      <Alert role="status" className="max-w-xl">
        <InfoIcon aria-hidden="true" />
        <AlertTitle>{en.admin.installUnavailable}</AlertTitle>
        <AlertDescription>{en.admin.installUnavailableDescription}</AlertDescription>
      </Alert>
    </AdminPage>
  )
}
