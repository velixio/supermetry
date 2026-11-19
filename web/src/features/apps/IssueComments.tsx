import { useState } from 'react'

import { InfoTip } from '@/components/InfoTip'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Textarea } from '@/components/ui/textarea'

import { useCommentMutations, useIssueComments } from './api'

const MAX_COMMENT = 10000

export function IssueComments({ siteId, issueId, canEdit }: { siteId: number; issueId: number; canEdit: boolean }) {
  const comments = useIssueComments(siteId, issueId)
  const { post, remove } = useCommentMutations(siteId, issueId)
  const [draft, setDraft] = useState('')
  const submit = () => post.mutate(draft.trim(), { onSuccess: () => setDraft('') })
  const error = post.error ?? remove.error
  return (
    <Card>
      <CardHeader><CardTitle>Comments <InfoTip label="About comments">Visible to everyone in the project. Do not paste secrets or personal data.</InfoTip></CardTitle></CardHeader>
      <CardContent className="flex flex-col gap-4">
        {comments.isError ? <p role="alert" className="text-sm text-destructive">{comments.error.message}</p> : null}
        {comments.isSuccess && comments.data.comments.length === 0 ? <p className="text-sm text-muted-foreground">No comments yet.</p> : null}
        <ul className="flex flex-col gap-3">
          {(comments.data?.comments ?? []).map((comment) => (
            <li key={comment.id} className="rounded-lg border p-3 text-sm">
              <div className="mb-1 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                <span><strong className="font-medium text-foreground">{comment.author_name || 'Removed user'}</strong> · {comment.created_at.slice(0, 16).replace('T', ' ')}{comment.edited_at ? ' (edited)' : ''}</span>
                {canEdit ? <Button type="button" size="sm" variant="ghost" aria-label={`Delete comment ${comment.id}`} disabled={remove.isPending} onClick={() => remove.mutate(comment.id)}>Delete</Button> : null}
              </div>
              <p className="whitespace-pre-wrap break-words">{comment.body}</p>
            </li>
          ))}
        </ul>
        {error ? <p role="alert" className="text-sm text-destructive">{error.message}</p> : null}
        {canEdit ? (
          <form className="flex flex-col gap-2" onSubmit={(event) => { event.preventDefault(); if (draft.trim()) submit() }}>
            <Textarea aria-label="Add a comment" value={draft} maxLength={MAX_COMMENT} onChange={(event) => setDraft(event.target.value)} placeholder="Write a comment" />
            <div><Button type="submit" disabled={post.isPending || draft.trim() === ''}>Comment</Button></div>
          </form>
        ) : null}
      </CardContent>
    </Card>
  )
}
