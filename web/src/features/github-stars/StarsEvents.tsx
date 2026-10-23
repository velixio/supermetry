import { useState, type FormEvent } from 'react'

import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { en } from '@/messages/en'
import { ErrorLine, formatDate } from '@/features/admin/AdminCommon'
import { useEraseStarsUser, useStarsEvents } from './githubStarsApi'

const loginPattern = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$/

export function StarsEvents({ repoId }: { repoId: number }) {
  const events = useStarsEvents(repoId)
  const erase = useEraseStarsUser(repoId)
  const [login, setLogin] = useState('')
  const [target, setTarget] = useState<string | null>(null)
  const [done, setDone] = useState<number | null>(null)
  const rows = events.data?.pages.flatMap((page) => page.data) ?? []
  const validLogin = loginPattern.test(login.trim())
  const ask = (value: string) => { erase.reset(); setDone(null); setTarget(value) }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (validLogin) ask(login.trim())
  }
  const confirm = () => {
    if (!target) return
    erase.mutate(target, { onSuccess: (result) => { setDone(result.deleted_events); setTarget(null); setLogin('') } })
  }
  return (
    <section aria-labelledby="stars-events" className="flex min-w-0 flex-col gap-3">
      <h2 id="stars-events" className="text-base font-semibold">{en.githubStars.events}</h2>
      {events.isError ? <ErrorLine error={events.error} /> : null}
      {events.isSuccess && rows.length === 0 ? (
        <Empty><EmptyHeader><EmptyTitle>{en.githubStars.eventsEmpty}</EmptyTitle><EmptyDescription>{en.githubStars.eventsEmptyHint}</EmptyDescription></EmptyHeader></Empty>
      ) : rows.length > 0 ? (
        <div className="overflow-x-auto rounded-lg border">
          <Table className="min-w-[36rem]">
            <TableHeader>
              <TableRow>
                <TableHead>{en.githubStars.user}</TableHead><TableHead>{en.githubStars.action}</TableHead>
                <TableHead>{en.githubStars.when}</TableHead><TableHead>{en.githubStars.source}</TableHead>
                <TableHead><span className="sr-only">{en.githubStars.erase}</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.id}>
                  <TableCell className="font-medium">{row.login}</TableCell>
                  <TableCell><Badge variant={row.action === 'starred' ? 'secondary' : 'outline'}>{row.action === 'starred' ? en.githubStars.starred : en.githubStars.unstarred}</Badge></TableCell>
                  <TableCell>{formatDate(row.occurred_at)}</TableCell>
                  <TableCell>{row.source}</TableCell>
                  <TableCell className="text-right">
                    <Button type="button" variant="ghost" size="sm" className="min-h-11" aria-label={en.githubStars.eraseRow(row.login)} onClick={() => ask(row.login)}>{en.githubStars.erase}</Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}
      {events.hasNextPage ? (
        <Button type="button" variant="outline" className="min-h-11 self-start" disabled={events.isFetchingNextPage} onClick={() => void events.fetchNextPage()}>
          {events.isFetchingNextPage ? en.githubStars.loadingMore : en.githubStars.loadMore}
        </Button>
      ) : null}

      <form className="flex flex-col gap-2 rounded-lg border p-3" onSubmit={submit}>
        <h3 className="text-sm font-semibold">{en.githubStars.erase}</h3>
        <p className="text-sm text-muted-foreground">{en.githubStars.eraseDescription}</p>
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex min-w-0 flex-1 basis-48 flex-col gap-1 text-sm font-medium">
            {en.githubStars.eraseLogin}
            <Input className="min-h-11" value={login} maxLength={39} autoComplete="off" onChange={(event) => setLogin(event.target.value)} />
          </label>
          <Button type="submit" variant="destructive" className="min-h-11" disabled={!validLogin}>{en.githubStars.erase}</Button>
        </div>
        {done !== null ? <p role="status" className="text-sm">{en.githubStars.erased(done)}</p> : null}
      </form>

      <AlertDialog open={target !== null} onOpenChange={(open) => { if (!open && !erase.isPending) setTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{en.githubStars.eraseTitle(target ?? '')}</AlertDialogTitle>
            <AlertDialogDescription>{en.githubStars.eraseBody}</AlertDialogDescription>
          </AlertDialogHeader>
          <ErrorLine error={erase.error} />
          <AlertDialogFooter>
            <AlertDialogCancel disabled={erase.isPending}>{en.githubStars.cancel}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={erase.isPending} onClick={(event) => { event.preventDefault(); confirm() }}>
              {erase.isPending ? en.githubStars.erasing : en.githubStars.eraseConfirm}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}
