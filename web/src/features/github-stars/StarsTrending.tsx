import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { ErrorState } from '@/components/ErrorState'
import { ErrorLine, formatDate } from '@/features/admin/AdminCommon'
import { useSetTrendingEnabled, useTrending, type TrendingSighting, type TrendingStatus } from './trendingApi'
import { trendingText as t } from './trendingMessages'

function listName(s: TrendingSighting, language: string) {
  if (!s.list_language) return t.allLanguages
  // Show the repository's own spelling of its language when this is its list.
  return language.toLowerCase() === s.list_language ? language : s.list_language
}

function windowName(s: TrendingSighting) {
  return s.since === 'daily' ? t.daily : t.weekly
}

function Episode({ s, language }: { s: TrendingSighting; language: string }) {
  const name = listName(s, language)
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
      <Badge variant="secondary" className="tabular-nums" aria-label={t.rankLabel(name, windowName(s), s.last_rank)}>
        {t.rank(s.last_rank)}
      </Badge>
      <span className="min-w-0 font-medium">{name} · {windowName(s)}</span>
      <span className="text-muted-foreground">{t.best(s.best_rank)}</span>
      <span className="text-muted-foreground">{s.left_at ? t.span(formatDate(s.first_seen_at), formatDate(s.left_at)) : t.from(formatDate(s.first_seen_at))}</span>
    </li>
  )
}

function statusLine(data: TrendingStatus) {
  switch (data.status) {
    case 'off': return t.off
    case 'waiting': return t.waiting
    case 'unreadable': return t.unreadable
    default: return data.last_checked_at ? t.watching(formatDate(data.last_checked_at)) : t.watchingNever
  }
}

/** The repository page's trending card: opt-in switch, watcher status, current appearances and history. */
export function StarsTrending({ repoId, canManage = true }: { repoId: number; canManage?: boolean }) {
  const trending = useTrending(repoId)
  const setEnabled = useSetTrendingEnabled(repoId)
  const data = trending.data
  const history = data?.episodes.filter((e) => e.left_at) ?? []
  return (
    <Card className="min-w-0" aria-labelledby="stars-trending">
      <CardHeader>
        <CardTitle id="stars-trending" className="text-base font-semibold">{t.title}</CardTitle>
        <p className="text-xs text-muted-foreground">{t.description}</p>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {trending.isError ? (
          <ErrorState detail={trending.error instanceof Error ? trending.error.message : undefined} onRetry={() => void trending.refetch()} />
        ) : !data ? <Skeleton aria-label={t.loading} className="h-16 w-full" /> : (
          <>
            {canManage ? (
              <label htmlFor="trending-enabled" className="flex min-h-11 w-fit items-center gap-2 pr-2 text-sm">
                <Switch id="trending-enabled" checked={data.enabled} disabled={setEnabled.isPending}
                  onCheckedChange={(enabled) => setEnabled.mutate(enabled)} />
                {t.enable}
              </label>
            ) : null}
            <p role="status" className={data.status === 'unreadable' ? 'text-sm text-destructive' : 'text-sm'}>{statusLine(data)}</p>
            {data.deferred_lists > 0 && data.enabled ? <p className="text-xs text-muted-foreground">{t.deferred(data.deferred_lists)}</p> : null}
            <ErrorLine error={setEnabled.error} />
            {data.enabled || data.episodes.length > 0 ? (
              <>
                <section aria-labelledby="trending-now" className="flex flex-col gap-1">
                  <h3 id="trending-now" className="text-sm font-medium">{t.current}</h3>
                  {data.current.length === 0 ? <p className="text-sm text-muted-foreground">{t.nowEmpty}</p> : (
                    <ul className="flex flex-col divide-y rounded-lg border">
                      {data.current.map((s) => <Episode key={s.id} s={s} language={data.language} />)}
                    </ul>
                  )}
                </section>
                <section aria-labelledby="trending-history" className="flex flex-col gap-1">
                  <h3 id="trending-history" className="text-sm font-medium">{t.history}</h3>
                  {history.length === 0 ? <p className="text-sm text-muted-foreground">{t.historyEmpty}</p> : (
                    <ul className="flex flex-col divide-y rounded-lg border">
                      {history.map((s) => <Episode key={s.id} s={s} language={data.language} />)}
                    </ul>
                  )}
                </section>
              </>
            ) : null}
            <p className="text-xs text-muted-foreground">{t.caveat}</p>
          </>
        )}
      </CardContent>
    </Card>
  )
}
