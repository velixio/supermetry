import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Undo2Icon } from 'lucide-react'

import { ConfirmDangerDialog } from '@/components/ConfirmDangerDialog'
import { StatusChip } from '@/components/StatusChip'
import { Button } from '@/components/ui/button'
import { fmt } from '@/lib/format'
import { importWizard } from '@/messages/importWizard'

import { importKeys, importsApi, type SiteImport } from './api'

const t = importWizard.history

function range(item: SiteImport): string {
  return item.date_from ? `${item.date_from.slice(0, 10)} – ${(item.date_to ?? item.date_from).slice(0, 10)}` : '—'
}

type Props = {
  siteId: number
  orgId: number
  items: SiteImport[] | undefined
  loading: boolean
  onReview: (item: SiteImport) => void
}

export function ImportHistory({ siteId, orgId, items, loading, onReview }: Props) {
  const client = useQueryClient()
  const remove = useMutation({
    mutationFn: (id: number) => importsApi.remove(id, orgId),
    onSuccess: () => client.invalidateQueries({ queryKey: importKeys.list(siteId) }),
  })
  return (
    <section aria-labelledby="import-history" className="flex flex-col gap-3">
      <h2 id="import-history" className="font-semibold">{t.heading}</h2>
      {loading ? <p className="text-sm text-muted-foreground">{t.loading}</p> : null}
      {items?.length === 0 ? <p className="text-sm text-muted-foreground">{t.empty}</p> : null}
      {items?.length ? (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead className="border-b text-muted-foreground">
              <tr>
                <th scope="col" className="py-2 pr-4 font-medium">{t.file}</th>
                <th scope="col" className="hidden py-2 pr-4 font-medium sm:table-cell">{t.source}</th>
                <th scope="col" className="py-2 pr-4 font-medium">{t.status}</th>
                <th scope="col" className="hidden py-2 pr-4 font-medium sm:table-cell">{t.rows}</th>
                <th scope="col" className="hidden py-2 pr-4 font-medium sm:table-cell">{t.range}</th>
                <th scope="col"><span className="sr-only">{t.actions}</span></th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.id} className="border-b last:border-0">
                  <td className="py-3 pr-4">
                    <p className="break-all font-medium">{item.file_name}</p>
                    {item.progress && (item.status === 'running' || item.status === 'pending') ? (
                      <p className="text-xs text-muted-foreground tabular-nums">{t.progress(item.progress.done_days, item.progress.total_days)}</p>
                    ) : null}
                    {item.error ? <p className="text-xs text-destructive">{item.error}</p> : null}
                  </td>
                  <td className="hidden py-3 pr-4 sm:table-cell">{t.sources[item.source] ?? item.source}</td>
                  <td className="py-3 pr-4">
                    <StatusChip variant={item.status === 'completed' ? 'success' : item.status === 'failed' ? 'danger' : 'neutral'}>
                      {importWizard.run.status[item.status]}
                    </StatusChip>
                  </td>
                  <td className="hidden py-3 pr-4 tabular-nums sm:table-cell">{fmt.number(item.rows)}</td>
                  <td className="hidden py-3 pr-4 tabular-nums sm:table-cell">{range(item)}</td>
                  <td className="py-3 text-right">
                    <div className="flex justify-end gap-1">
                      {item.status === 'completed' ? (
                        <Button type="button" variant="ghost" className="max-sm:min-h-11 max-sm:min-w-11" aria-label={t.reviewFor(item.file_name)} onClick={() => onReview(item)}>{t.review}</Button>
                      ) : null}
                      <ConfirmDangerDialog
                        trigger={
                          <Button type="button" variant="ghost" className="max-sm:min-h-11 max-sm:min-w-11" aria-label={importWizard.undo.actionFor(item.file_name)} disabled={remove.isPending}>
                            <Undo2Icon data-icon="inline-start" />{importWizard.undo.action}
                          </Button>
                        }
                        title={importWizard.undo.title}
                        description={importWizard.undo.description(item.file_name)}
                        confirmLabel={importWizard.undo.confirm}
                        onConfirm={() => remove.mutateAsync(item.id)}
                      />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      {remove.isError ? <p role="alert" className="text-sm text-destructive">{importWizard.undo.failed}</p> : null}
    </section>
  )
}
