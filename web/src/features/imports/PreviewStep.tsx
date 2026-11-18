import { Button } from '@/components/ui/button'
import { fmt } from '@/lib/format'
import { importWizard } from '@/messages/importWizard'

import type { PullPreview } from './api'

const t = importWizard.preview
const MAX_ERRORS = 5

type Props = {
  preview?: PullPreview
  loading: boolean
  error?: string
  /** The preview covers fewer days than the import (long API ranges). */
  partial?: { shown: number; total: number }
  starting: boolean
  startError?: string
  onRetry: () => void
  onStart: () => void
  onChangeRange: () => void
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col rounded-lg border p-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-lg font-semibold tabular-nums">{value}</dd>
    </div>
  )
}

export function PreviewStep({ preview, loading, error, partial, starting, startError, onRetry, onStart, onChangeRange }: Props) {
  if (loading) return <p role="status" className="text-sm text-muted-foreground">{t.loading}</p>
  if (error || !preview) {
    return (
      <div className="flex flex-col gap-3">
        <p role="alert" className="text-sm text-destructive">{error ?? t.failed}</p>
        <Button type="button" variant="outline" className="self-start" onClick={onRetry}>{t.retry}</Button>
      </div>
    )
  }
  const { overlap } = preview
  const found = Object.entries(preview.dimensions).filter(([, count]) => count > 0).sort(([a], [b]) => a.localeCompare(b))
  const replacing = overlap.same_file != null || overlap.imported_days > 0
  const range = preview.date_from ? `${preview.date_from.slice(0, 10)} – ${(preview.date_to ?? preview.date_from).slice(0, 10)}` : t.noRange
  return (
    <div className="flex flex-col gap-5">
      <h3 className="font-medium">{t.heading}</h3>
      {partial ? <p className="text-sm text-muted-foreground">{t.partial(partial.shown, partial.total)}</p> : null}
      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label={t.rows} value={fmt.number(preview.rows)} />
        <Stat label={t.days} value={fmt.number(preview.days)} />
        <Stat label={t.visitors} value={fmt.number(preview.totals.visitors)} />
        <Stat label={t.pageviews} value={fmt.number(preview.totals.pageviews)} />
        <Stat label={t.visits} value={fmt.number(preview.totals.visits)} />
        <Stat label={t.events} value={fmt.number(preview.totals.events)} />
        <Stat label={t.skipped} value={fmt.number(preview.bad_rows)} />
        <Stat label={t.duplicates} value={fmt.number(preview.duplicate_rows)} />
      </dl>
      <p className="text-sm"><span className="text-muted-foreground">{t.range}: </span>{range}</p>

      <section aria-labelledby="preview-found" className="flex flex-col gap-2">
        <h4 id="preview-found" className="text-sm font-medium">{t.foundHeading}</h4>
        {found.length === 0 ? <p className="text-sm text-muted-foreground">{t.nothingFound}</p> : (
          <ul className="grid gap-1 text-sm sm:grid-cols-2">
            {found.map(([name, count]) => (
              <li key={name} className="flex justify-between gap-4 border-b py-1">
                <span>{t.breakdowns[name] ?? name}</span>
                <span className="tabular-nums text-muted-foreground">{fmt.number(count)}</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      {preview.errors.length > 0 ? (
        <section aria-labelledby="preview-errors" className="flex flex-col gap-1 text-sm">
          <h4 id="preview-errors" className="font-medium">{t.errorsHeading}</h4>
          <ul className="list-disc pl-5 text-muted-foreground">
            {preview.errors.slice(0, MAX_ERRORS).map((item) => <li key={`${item.row}-${item.message}`}>{t.errorRow(item.row, item.message)}</li>)}
          </ul>
          {preview.bad_rows > MAX_ERRORS ? <p className="text-xs text-muted-foreground">{t.errorsMore(MAX_ERRORS, preview.bad_rows)}</p> : null}
        </section>
      ) : null}

      <section aria-labelledby="preview-overlap" className="flex flex-col gap-2 rounded-lg border bg-muted/40 p-4 text-sm">
        <h4 id="preview-overlap" className="font-medium">{t.overlapHeading}</h4>
        {overlap.imported_days === 0 && overlap.live_days === 0 && !overlap.same_file ? <p>{t.overlapNone}</p> : null}
        {overlap.same_file ? <p>{t.sameFile(overlap.same_file.imported_at.slice(0, 10))}</p> : null}
        {overlap.imported_days > 0 ? <p>{t.overlapImported(overlap.imported_days)}</p> : null}
        {overlap.live_days > 0 ? <p>{t.overlapLive(overlap.live_days)}</p> : null}
      </section>

      <div role="group" aria-labelledby="preview-choice" className="flex flex-col gap-3">
        <h4 id="preview-choice" className="text-sm font-medium">{t.choiceHeading}</h4>
        <p className="text-sm text-muted-foreground">{t.estimate(partial?.total ?? preview.days)}</p>
        <div className="flex flex-wrap gap-2">
          <Button type="button" disabled={starting} onClick={onStart}>
            {starting ? t.starting : replacing ? t.continueReplace : t.continueRange}
          </Button>
          <Button type="button" variant="outline" disabled={starting} onClick={onChangeRange}>{t.changeRange}</Button>
        </div>
        {startError ? <p role="alert" className="text-sm text-destructive">{startError}</p> : null}
      </div>
    </div>
  )
}
