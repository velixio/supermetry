import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { en } from '@/messages/en'
import { formatDate } from './AdminCommon'
import type { AdminUpdateJob } from './adminApi'

function resultVariant(status: AdminUpdateJob['status']) {
  if (status === 'complete') return 'secondary' as const
  if (status === 'failed' || status === 'recovery_required') return 'destructive' as const
  return 'outline' as const
}

// UpdateHistory lists the most recent update attempts, newest first, below the update card.
export function UpdateHistory({ jobs }: { jobs: AdminUpdateJob[] }) {
  return (
    <Card className="max-w-xl">
      <CardHeader>
        <CardTitle>{en.admin.updateHistory}</CardTitle>
        <CardDescription>{en.admin.updateHistoryDescription}</CardDescription>
      </CardHeader>
      <CardContent>
        {jobs.length === 0 ? (
          <p className="text-sm text-muted-foreground">{en.admin.updateHistoryEmpty}</p>
        ) : (
          <Table aria-label={en.admin.updateHistory}>
            <TableHeader>
              <TableRow>
                <TableHead>{en.admin.historyVersion}</TableHead>
                <TableHead>{en.admin.historyResult}</TableHead>
                <TableHead>{en.admin.historyWhen}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {jobs.map((job) => (
                <TableRow key={job.id}>
                  <TableCell className="font-mono text-xs">{job.from_version} → {job.target_version}</TableCell>
                  <TableCell><Badge variant={resultVariant(job.status)}>{en.admin.updateStages[job.status]}</Badge></TableCell>
                  <TableCell className="text-muted-foreground">{formatDate(job.finished_at ?? job.created_at)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}
