import { InfoTip } from '@/components/InfoTip'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

import { useIssueFeedback } from './api'

/** What users wrote when the app asked them what went wrong (captureFeedback or a user report). */
export function IssueFeedback({ siteId, issueId }: { siteId: number; issueId: number }) {
  const feedback = useIssueFeedback(siteId, issueId)
  const rows = feedback.data?.pages.flatMap((page) => page.feedback ?? []) ?? []
  return (
    <Card>
      <CardHeader>
        <CardTitle>User feedback <InfoTip label="About user feedback">Reports from people who hit this issue. Text is scrubbed; names and email addresses are not stored.</InfoTip></CardTitle>
      </CardHeader>
      <CardContent>
        {feedback.isPending ? <p role="status" className="text-sm text-muted-foreground">Loading feedback…</p> : null}
        {feedback.isError ? <p role="alert" className="text-sm text-destructive">{feedback.error.message}</p> : null}
        {feedback.isSuccess && rows.length === 0 ? <p className="text-sm text-muted-foreground">No feedback for this issue yet.</p> : null}
        <ul aria-label="User feedback" className="flex flex-col gap-3">
          {rows.map((item) => (
            <li key={item.id} className="rounded-lg border p-3 text-sm">
              <div className="mb-1 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
                <time dateTime={item.at} className="tabular-nums">{item.at.slice(0, 16).replace('T', ' ')}</time>
                {item.url ? <span>on {item.url}</span> : null}
                {item.has_contact ? <span>left a contact address</span> : null}
              </div>
              <p className="whitespace-pre-wrap break-words">{item.message}</p>
            </li>
          ))}
        </ul>
        {feedback.hasNextPage ? <div className="mt-3"><Button type="button" size="sm" variant="outline" disabled={feedback.isFetchingNextPage} onClick={() => void feedback.fetchNextPage()}>Load older feedback</Button></div> : null}
      </CardContent>
    </Card>
  )
}
