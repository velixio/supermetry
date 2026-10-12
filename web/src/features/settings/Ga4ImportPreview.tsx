import { Button } from '@/components/ui/button'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import type { ImportPreview } from './privateApi'

const t = en.ga4Import

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5 rounded-lg border bg-background p-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-lg font-semibold tabular-nums">{value}</dd>
    </div>
  )
}

export function Ga4Format() {
  return (
    <div className="flex flex-col gap-2 rounded-lg border bg-muted/40 p-4 text-sm">
      <h3 className="font-medium">{t.formatTitle}</h3>
      <ul className="list-disc space-y-1 pl-5 text-muted-foreground">
        <li>{t.formatDate}</li>
        <li>{t.formatMetrics}</li>
        <li>{t.formatDimensions}</li>
        <li>{t.formatTotals}</li>
      </ul>
    </div>
  )
}

export function Ga4ImportPreview({
  preview,
  pending,
  onConfirm,
  onCancel,
}: {
  preview: ImportPreview
  pending: boolean
  onConfirm: () => void
  onCancel: () => void
}) {
  const range = preview.date_from
    ? `${preview.date_from} – ${preview.date_to}`
    : t.noRange
  const sameFile = preview.overlap.same_file
  const breakdowns = Object.entries(preview.dimensions)
    .filter(([, count]) => count > 0)
    .sort(([a], [b]) => a.localeCompare(b))
  return (
    <section
      aria-labelledby="import-preview-heading"
      className="flex flex-col gap-4 rounded-lg border border-primary/40 bg-card p-4"
    >
      <h3 id="import-preview-heading" className="font-medium">
        {t.previewHeading}
      </h3>
      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label={t.rowsAccepted} value={fmt.number(preview.rows)} />
        <Stat label={t.rowsSkipped} value={fmt.number(preview.bad_rows)} />
        <Stat label={t.rowsDuplicate} value={fmt.number(preview.duplicate_rows)} />
        <Stat label={t.days} value={fmt.number(preview.days)} />
        <Stat label={t.visitors} value={fmt.number(preview.totals.visitors)} />
        <Stat label={t.visits} value={fmt.number(preview.totals.visits)} />
        <Stat label={t.pageviews} value={fmt.number(preview.totals.pageviews)} />
        <Stat label={t.events} value={fmt.number(preview.totals.events)} />
      </dl>
      <p className="text-sm">
        <span className="text-muted-foreground">{t.range}: </span>
        <span className="font-medium tabular-nums">{range}</span>
      </p>
      {breakdowns.length ? (
        <p className="text-sm">
          <span className="text-muted-foreground">{t.breakdowns}: </span>
          {breakdowns.map(([name, count]) => `${name.replace('_', ' ')} (${fmt.number(count)})`).join(', ')}
        </p>
      ) : null}
      {preview.errors.length ? (
        <div className="flex flex-col gap-1">
          <h4 className="text-sm font-medium">{t.errorsHeading}</h4>
          <ul className="list-disc space-y-0.5 pl-5 text-sm text-destructive">
            {preview.errors.map((error) => (
              <li key={`${error.row}-${error.message}`}>{t.errorRow(error.row, error.message)}</li>
            ))}
          </ul>
          {preview.bad_rows > preview.errors.length ? (
            <p className="text-xs text-muted-foreground">
              {t.errorsMore(preview.errors.length, preview.bad_rows)}
            </p>
          ) : null}
        </div>
      ) : null}
      <div className="flex flex-col gap-1 text-sm" role="status">
        {sameFile ? <p>{t.sameFile(sameFile.imported_at.slice(0, 10))}</p> : null}
        {preview.overlap.imported_days > 0 ? <p>{t.overlapImported(preview.overlap.imported_days)}</p> : null}
        {preview.overlap.live_days > 0 ? <p>{t.overlapLive(preview.overlap.live_days)}</p> : null}
      </div>
      <div className="flex flex-wrap gap-2">
        <Button type="button" disabled={pending || preview.rows === 0} onClick={onConfirm}>
          {sameFile ? t.confirmReplace : t.confirm}
        </Button>
        <Button type="button" variant="outline" disabled={pending} onClick={onCancel}>
          {t.chooseAnother}
        </Button>
      </div>
    </section>
  )
}
