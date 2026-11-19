import { useId, useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { StatusChip } from '@/components/StatusChip'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { fmt } from '@/lib/format'
import { importWizard } from '@/messages/importWizard'

import { importKeys, importsApi } from './api'
import { addDays, dayCount, isoDay } from './draft'
import { signedPercent, toleranceOf, type Tolerance } from './reconcile'
import { toleranceVariant } from './ReviewStep'

const t = importWizard.switchOver
const MAX_DAYS = 400

/** Imported (old tool) against natively collected numbers per day, to build trust before removing the old script. */
export function SwitchOverPanel({ siteId, orgId, today = new Date() }: { siteId: number; orgId: number; today?: Date }) {
  const id = useId()
  const [to, setTo] = useState(isoDay(today))
  const [from, setFrom] = useState(addDays(isoDay(today), -29))
  const valid = dayCount(from, to) > 0 && dayCount(from, to) <= MAX_DAYS
  const compare = useQuery({
    queryKey: importKeys.compare(siteId, from, to),
    queryFn: () => importsApi.compare(siteId, orgId, from, to),
    enabled: valid,
  })
  const days = (compare.data?.days ?? []).filter((day) => day.native !== null)
  const worst = (a: Tolerance, b: Tolerance) => (['none', 'within', 'close', 'differs'].indexOf(a) >= ['none', 'within', 'close', 'differs'].indexOf(b) ? a : b)
  const tolerances = days.map((day) => worst(toleranceOf(day.visitors_diff_pct), toleranceOf(day.pageviews_diff_pct)))
  const within = tolerances.filter((value) => value === 'within').length
  const differs = tolerances.filter((value) => value === 'differs').length
  const verdict: Tolerance = days.length === 0 ? 'none' : differs > days.length / 4 ? 'differs' : within >= days.length * 0.75 ? 'within' : 'close'

  return (
    <section aria-labelledby={`${id}-h`} className="flex flex-col gap-4">
      <header className="flex flex-col gap-1">
        <h2 id={`${id}-h`} className="font-semibold">{t.heading}</h2>
        <p className="text-sm text-muted-foreground">{t.intro}</p>
      </header>
      <FieldGroup className="sm:flex-row">
        <Field>
          <FieldLabel htmlFor={`${id}-from`}>{t.from}</FieldLabel>
          <Input id={`${id}-from`} type="date" value={from} onChange={(event) => setFrom(event.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-to`}>{t.to}</FieldLabel>
          <Input id={`${id}-to`} type="date" value={to} onChange={(event) => setTo(event.target.value)} />
        </Field>
      </FieldGroup>
      {compare.isLoading ? <p role="status" className="text-sm text-muted-foreground">{t.loading}</p> : null}
      {compare.isError ? <p role="alert" className="text-sm text-destructive">{t.failed}</p> : null}
      {compare.data && days.length === 0 ? <p className="text-sm text-muted-foreground">{t.none}</p> : null}
      {days.length > 0 ? (
        <>
          <p className="flex flex-wrap items-center gap-2 text-sm" role="status">
            {t.summary(days.length, within)}
            <StatusChip variant={toleranceVariant[verdict]}>
              {verdict === 'within' ? t.verdictGood : verdict === 'differs' ? t.verdictDiffers : t.verdictCheck}
            </StatusChip>
          </p>
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <caption className="sr-only">{t.heading}</caption>
              <thead className="border-b text-muted-foreground">
                <tr>
                  <th scope="col" className="py-2 pr-4 font-medium">{t.day}</th>
                  <th scope="col" className="py-2 pr-4 text-right font-medium">{t.oldVisitors}</th>
                  <th scope="col" className="py-2 pr-4 text-right font-medium">{t.newVisitors}</th>
                  <th scope="col" className="py-2 pr-4 text-right font-medium">{t.visitorsDiff}</th>
                  <th scope="col" className="py-2 pr-4 text-right font-medium">{t.oldPageviews}</th>
                  <th scope="col" className="py-2 pr-4 text-right font-medium">{t.newPageviews}</th>
                  <th scope="col" className="py-2 text-right font-medium">{t.pageviewsDiff}</th>
                </tr>
              </thead>
              <tbody>
                {days.map((day) => (
                  <tr key={day.day} className="border-b last:border-0">
                    <th scope="row" className="py-2 pr-4 font-medium tabular-nums">{day.day}</th>
                    <td className="py-2 pr-4 text-right tabular-nums">{fmt.number(day.imported.visitors)}</td>
                    <td className="py-2 pr-4 text-right tabular-nums">{fmt.number(day.native?.visitors ?? 0)}</td>
                    <td className="py-2 pr-4 text-right tabular-nums">{signedPercent(day.visitors_diff_pct, t.n)}</td>
                    <td className="py-2 pr-4 text-right tabular-nums">{fmt.number(day.imported.pageviews)}</td>
                    <td className="py-2 pr-4 text-right tabular-nums">{fmt.number(day.native?.pageviews ?? 0)}</td>
                    <td className="py-2 text-right tabular-nums">{signedPercent(day.pageviews_diff_pct, t.n)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="text-xs text-muted-foreground">{t.differenceNote}</p>
        </>
      ) : null}
    </section>
  )
}
