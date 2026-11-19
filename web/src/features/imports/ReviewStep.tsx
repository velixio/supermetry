import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Undo2Icon } from 'lucide-react'

import { ConfirmDangerDialog } from '@/components/ConfirmDangerDialog'
import { StatusChip, type StatusChipVariant } from '@/components/StatusChip'
import { Button } from '@/components/ui/button'
import { fmt } from '@/lib/format'
import { importWizard } from '@/messages/importWizard'

import { importKeys, importsApi, type SiteImport } from './api'
import { addDays } from './draft'
import { monthlyRows, monthTolerance, signedPercent, TOLERANCE_CLOSE, TOLERANCE_WITHIN, type Tolerance } from './reconcile'

const t = importWizard.review
const MAX_COMPARE_DAYS = 400

export const toleranceVariant: Record<Tolerance, StatusChipVariant> = {
  within: 'success', close: 'warning', differs: 'danger', none: 'neutral',
}

type Props = {
  siteId: number
  orgId: number
  item: SiteImport
  onUndone: () => void
  onAnother: () => void
}

export function ReviewStep({ siteId, orgId, item, onUndone, onAnother }: Props) {
  const client = useQueryClient()
  const to = (item.date_to ?? item.date_from ?? '').slice(0, 10)
  const rawFrom = (item.date_from ?? to).slice(0, 10)
  const from = rawFrom < addDays(to, -(MAX_COMPARE_DAYS - 1)) ? addDays(to, -(MAX_COMPARE_DAYS - 1)) : rawFrom
  const compare = useQuery({
    queryKey: importKeys.compare(siteId, from, to, item.id),
    queryFn: () => importsApi.compare(siteId, orgId, from, to, item.id),
    enabled: to !== '',
  })
  const remove = useMutation({
    mutationFn: () => importsApi.remove(item.id, orgId),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: importKeys.list(siteId) })
      onUndone()
    },
  })
  const rows = monthlyRows(compare.data?.days ?? [])
  const name = item.file_name

  return (
    <div className="flex flex-col gap-5">
      <h3 className="font-medium">{t.heading}</h3>
      {to ? <p className="text-sm">{t.summary(item.rows, rawFrom, to)}</p> : null}
      {item.error ? <p className="text-sm text-warning">{t.warning} {item.error}</p> : null}
      <p className="text-sm text-muted-foreground">{t.intro}</p>
      {compare.isLoading ? <p role="status" className="text-sm text-muted-foreground">{t.loading}</p> : null}
      {compare.isError ? <p role="alert" className="text-sm text-destructive">{t.failed}</p> : null}
      {compare.data && rows.length === 0 ? <p className="text-sm">{t.noOverlap}</p> : null}
      {rows.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <caption className="sr-only">{t.heading}</caption>
            <thead className="text-muted-foreground">
              <tr>
                <th scope="col" className="py-2 pr-4 font-medium">{t.month}</th>
                <th scope="col" className="py-2 pr-4 text-right font-medium">{t.comparedDays}</th>
                <th scope="col" className="py-2 pr-4 text-right font-medium">{t.importedVisitors}</th>
                <th scope="col" className="py-2 pr-4 text-right font-medium">{t.collectedVisitors}</th>
                <th scope="col" className="py-2 pr-4 text-right font-medium">{t.importedPageviews}</th>
                <th scope="col" className="py-2 pr-4 text-right font-medium">{t.collectedPageviews}</th>
                <th scope="col" className="py-2 font-medium">{t.visitorsDiff}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const tolerance = monthTolerance(row)
                return (
                  <tr key={row.month} className="">
                    <th scope="row" className="py-2 pr-4 font-medium tabular-nums">{row.month}</th>
                    <td className="py-2 pr-4 text-right tabular-nums">{row.comparedDays}</td>
                    <td className="py-2 pr-4 text-right tabular-nums">{fmt.number(row.importedVisitors)}</td>
                    <td className="py-2 pr-4 text-right tabular-nums">{fmt.number(row.nativeVisitors)}</td>
                    <td className="py-2 pr-4 text-right tabular-nums">{fmt.number(row.importedPageviews)}</td>
                    <td className="py-2 pr-4 text-right tabular-nums">{fmt.number(row.nativePageviews)}</td>
                    <td className="py-2">
                      <StatusChip variant={toleranceVariant[tolerance]}>
                        {t.tolerance[tolerance]}
                        {tolerance === 'none' ? '' : `: ${signedPercent(row.visitorsDiff, importWizard.switchOver.n)} / ${signedPercent(row.pageviewsDiff, importWizard.switchOver.n)}`}
                      </StatusChip>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
          <p className="mt-2 text-xs text-muted-foreground">{t.sumNote} {t.toleranceNote(TOLERANCE_WITHIN, TOLERANCE_CLOSE)}</p>
        </div>
      ) : null}
      <p className="text-sm text-muted-foreground">{t.switchHint}</p>

      <section aria-labelledby="undo-heading" className="flex flex-col gap-2 pt-4">
        <h4 id="undo-heading" className="text-sm font-medium">{t.undoHeading}</h4>
        <p className="text-sm text-muted-foreground">{t.undoHelp}</p>
        <div className="flex flex-wrap gap-2">
          <ConfirmDangerDialog
            trigger={<Button type="button" variant="outline" disabled={remove.isPending}><Undo2Icon data-icon="inline-start" />{importWizard.undo.action}</Button>}
            title={importWizard.undo.title}
            description={importWizard.undo.description(name)}
            confirmLabel={importWizard.undo.confirm}
            onConfirm={() => remove.mutateAsync()}
          />
          <Button type="button" onClick={onAnother}>{t.another}</Button>
        </div>
        {remove.isError ? <p role="alert" className="text-sm text-destructive">{importWizard.undo.failed}</p> : null}
      </section>
    </div>
  )
}
