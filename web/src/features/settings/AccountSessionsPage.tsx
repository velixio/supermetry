import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { Flag } from '@/components/Flag'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { BrowserLabel } from '@/components/visitor/BrowserLabel'
import { countryName, normalizeCountryCode } from '@/lib/country'

import { privateRequest, type ListResponse } from './privateApi'

type AccountSession = {
  ID: string
  CreatedAt?: string
  LastSeenAt?: string
  DeviceLabel?: string
  IPAddress?: string
  CountryCode?: string
  Browser?: string
  Current?: boolean
}

function timestamp(value?: string): Date | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isFinite(date.getTime()) && date.getUTCFullYear() > 1 ? date : undefined
}

function SessionTime({ value }: { value?: string }) {
  const date = timestamp(value)
  if (!date) return <span className="text-muted-foreground">Unknown</span>
  const local = date.toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: 'numeric', minute: '2-digit', second: '2-digit', timeZoneName: 'short',
  })
  const exact = `${local} (${date.toISOString()})`
  return <time dateTime={date.toISOString()} title={exact} aria-label={exact}>{local}</time>
}

function SessionCountry({ code = '' }: { code?: string }) {
  const normalized = normalizeCountryCode(code)
  const name = countryName(code)
  if (!normalized || normalized === 'ZZ' || name === 'Unknown') return <span className="text-muted-foreground">Unknown</span>
  return <span className="flex items-center gap-2"><Flag code={normalized} /><span>{name} ({normalized})</span></span>
}

const sessionsKey = ['sessions']

export function AccountSessionsPage() {
  const queryClient = useQueryClient()
  const sessions = useQuery({
    queryKey: sessionsKey,
    queryFn: () => privateRequest<ListResponse<AccountSession>>('GET', '/api/private/me/sessions'),
  })
  const revoke = useMutation({
    mutationFn: (id: string) => privateRequest<void>('DELETE', `/api/private/me/sessions/${encodeURIComponent(id)}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: sessionsKey }),
  })
  const ordered = [...(sessions.data?.data ?? [])].sort((a, b) =>
    Number(Boolean(b.Current)) - Number(Boolean(a.Current)) ||
    (timestamp(b.LastSeenAt)?.getTime() ?? 0) - (timestamp(a.LastSeenAt)?.getTime() ?? 0) ||
    (timestamp(b.CreatedAt)?.getTime() ?? 0) - (timestamp(a.CreatedAt)?.getTime() ?? 0),
  )

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">Active sessions</h1>
        <p className="text-sm text-muted-foreground">Review devices signed into your account and revoke any you do not recognize.</p>
      </header>
      <section aria-label="Account sessions" className="min-w-0 rounded-xl border bg-card p-5 text-card-foreground">
        <p className="mb-5 text-sm text-muted-foreground">IP address, approximate country and browser are recorded at sign-in. Times use your local timezone. Last active reflects authenticated activity, updated at most once a minute; known background polling does not update it.</p>
        {sessions.isPending && <p role="status">Loading sessions…</p>}
        {sessions.isError && <div role="alert" className="flex flex-wrap items-center gap-3"><p>Could not load your sessions.</p><Button variant="outline" size="sm" onClick={() => void sessions.refetch()}>Try again</Button></div>}
        {revoke.isError && <p role="alert" className="mb-4 text-sm text-destructive">Could not revoke this session. Try again.</p>}
        {sessions.isSuccess && ordered.length === 0 && <p className="text-sm text-muted-foreground">No active sessions.</p>}
        <ul className="divide-y" aria-label="Active account sessions">
          {ordered.map((session) => (
            <li key={session.ID} className="min-w-0 py-5 first:pt-0 last:pb-0">
              <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-3 text-sm font-medium">
                    <BrowserLabel value={session.Browser || 'Unknown browser'} />
                    {session.Current && <Badge variant="secondary">Current session</Badge>}
                  </div>
                  {session.DeviceLabel?.trim() && session.DeviceLabel.trim().toLowerCase() !== (session.Browser?.trim() || 'Unknown browser').toLowerCase() && (
                    <p className="mt-1 break-words text-sm text-muted-foreground">{session.DeviceLabel.trim()}</p>
                  )}
                </div>
                <Button size="sm" variant="outline" disabled={revoke.isPending || !session.ID} onClick={() => revoke.mutate(session.ID)} aria-label={session.Current ? 'Revoke current session' : `Revoke ${session.Browser || 'unknown browser'} session${session.IPAddress ? ` at ${session.IPAddress}` : ''}`}>
                  {revoke.isPending && revoke.variables === session.ID ? 'Revoking…' : 'Revoke'}
                </Button>
              </div>
              <dl className="grid min-w-0 grid-cols-1 gap-x-6 gap-y-4 text-sm sm:grid-cols-2">
                <div className="min-w-0"><dt className="mb-1 text-xs text-muted-foreground">IP address at sign-in</dt><dd className="break-all">{session.IPAddress || <span className="text-muted-foreground">Unknown</span>}</dd></div>
                <div className="min-w-0"><dt className="mb-1 text-xs text-muted-foreground">Approximate country</dt><dd><SessionCountry code={session.CountryCode} /></dd></div>
                <div><dt className="mb-1 text-xs text-muted-foreground">Created</dt><dd><SessionTime value={session.CreatedAt} /></dd></div>
                <div><dt className="mb-1 text-xs text-muted-foreground">Last active</dt><dd><SessionTime value={session.LastSeenAt} /></dd></div>
              </dl>
            </li>
          ))}
        </ul>
      </section>
    </div>
  )
}
