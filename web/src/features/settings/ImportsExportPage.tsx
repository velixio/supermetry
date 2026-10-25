import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useCapability } from '@/lib/authorization'
import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { DownloadIcon, Undo2Icon, UploadIcon } from 'lucide-react'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import { EventExportFormat, eventExportAccept, isEventExportSource } from './EventExportFormat'
import { Ga4Format, Ga4ImportPreview } from './Ga4ImportPreview'

import {
  downloadSiteExport,
  previewSiteImport,
  privateRequest,
  uploadSiteImport,
  type ImportPreview,
  type ImportSource,
} from './privateApi'

type ImportStatus = 'pending' | 'running' | 'completed' | 'failed'

export type SiteImport = {
  id: number
  site_id: number
  source: ImportSource
  status: ImportStatus
  file_name: string
  date_from?: string
  date_to?: string
  rows: number
  error?: string
  created_by: number
  created_at: string
  completed_at?: string
}

export function importPollInterval(imports: Array<{ status: string }>): 2_000 | false {
  return imports.some(({ status }) => status === 'pending' || status === 'running')
    ? 2_000
    : false
}

const sourceLabels: Record<ImportSource, string> = {
  plausible: 'Plausible',
  umami: 'Umami',
  ga4: 'GA4',
  mixpanel: 'Mixpanel',
  amplitude: 'Amplitude',
}

// Plausible and Umami upload straight away; the other providers show a dry run
// first.
function previewsFirst(source: ImportSource): boolean {
  return source !== 'plausible' && source !== 'umami'
}

function titleCase(value: string): string {
  return `${value.charAt(0).toUpperCase()}${value.slice(1)}`
}

function today(): string {
  return new Date().toISOString().slice(0, 10)
}

function Surface({
  title,
  description,
  children,
}: {
  title: string
  description: string
  children: React.ReactNode
}) {
  return (
    <section className="flex flex-col gap-4 rounded-xl border bg-card p-4 md:p-5 text-card-foreground">
      <header className="flex flex-col gap-1">
        <h2 className="font-semibold">{title}</h2>
        <p className="text-sm text-muted-foreground">{description}</p>
      </header>
      {children}
    </section>
  )
}

export function ImportsExportPage({ siteId, orgId }: { siteId: number; orgId: number }) {
  const canExport = useCapability('data.export')
  const client = useQueryClient()
  const [source, setSource] = useState<ImportSource>('plausible')
  const [file, setFile] = useState<File>()
  const [progress, setProgress] = useState(0)
  const [preview, setPreview] = useState<ImportPreview>()
  const [undoTarget, setUndoTarget] = useState<SiteImport>()
  const imports = useQuery({
    queryKey: ['site-imports', siteId],
    queryFn: () =>
      privateRequest<SiteImport[]>('GET', `/api/private/sites/${siteId}/imports`, {
        orgId,
      }),
    refetchInterval: (query) => importPollInterval(query.state.data ?? []),
  })
  const upload = useMutation({
    mutationFn: () => {
      if (!file) throw new Error('Choose a file to import')
      return uploadSiteImport<SiteImport>(
        siteId,
        orgId,
        source,
        file,
        setProgress,
        undefined,
        preview?.overlap.same_file != null,
      )
    },
    onSuccess: () => {
      setPreview(undefined)
      return client.invalidateQueries({ queryKey: ['site-imports', siteId] })
    },
  })
  const dryRun = useMutation({
    mutationFn: () => {
      if (!file) throw new Error('Choose a file to import')
      return previewSiteImport(siteId, orgId, source, file)
    },
    onSuccess: setPreview,
  })
  const remove = useMutation({
    mutationFn: (id: number) =>
      privateRequest<void>('DELETE', `/api/private/imports/${id}`, { orgId }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['site-imports', siteId] }),
  })
  const exportData = useMutation({
    mutationFn: ({ from, to }: { from: string; to: string }) =>
      downloadSiteExport(siteId, orgId, from, to),
  })

  function submitImport(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setProgress(0)
    if (previewsFirst(source)) dryRun.mutate()
    else upload.mutate()
  }

  function resetPreview() {
    setPreview(undefined)
    dryRun.reset()
    upload.reset()
  }

  function submitExport(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canExport) return
    const data = new FormData(event.currentTarget)
    exportData.mutate({
      from: String(data.get('from') ?? ''),
      to: String(data.get('to') ?? ''),
    })
  }

  return (
    <PageContainer width="narrow">
      <PageHeader title="Imports &amp; export" description="Bring historical aggregate data into this site or download a portable ZIP." />

      <Surface
        title="Import historical data"
        description="Plausible ZIP exports, Umami website-event CSVs, GA4 report CSVs and Mixpanel or Amplitude event exports are supported. Imports never replace native data."
      >
        <form className="flex flex-col gap-5" onSubmit={submitImport}>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="import-source">Import source</FieldLabel>
              <select
                id="import-source"
                className="h-9 rounded-lg border bg-background px-3 text-sm"
                value={source}
                onChange={(event) => {
                  setSource(event.target.value as ImportSource)
                  resetPreview()
                }}
              >
                <option value="plausible">Plausible</option>
                <option value="umami">Umami</option>
                <option value="ga4">{en.ga4Import.provider}</option>
                <option value="mixpanel">{en.eventExportImport.mixpanelProvider}</option>
                <option value="amplitude">{en.eventExportImport.amplitudeProvider}</option>
              </select>
              {source === 'ga4' ? (
                <FieldDescription>{en.ga4Import.providerHelp}</FieldDescription>
              ) : null}
              {isEventExportSource(source) ? (
                <FieldDescription>{en.eventExportImport.providerHelp}</FieldDescription>
              ) : null}
            </Field>
            <Field>
              <FieldLabel htmlFor="import-file">Import file</FieldLabel>
              <Input
                id="import-file"
                type="file"
                accept={
                  isEventExportSource(source)
                    ? eventExportAccept[source]
                    : source === 'plausible'
                      ? '.zip,application/zip'
                      : '.csv,text/csv'
                }
                onChange={(event) => {
                  setFile(event.target.files?.[0])
                  resetPreview()
                }}
              />
              <FieldDescription>Maximum file size: 512 MB.</FieldDescription>
            </Field>
          </FieldGroup>
          {source === 'ga4' ? <Ga4Format /> : null}
          {isEventExportSource(source) ? <EventExportFormat source={source} /> : null}
          {preview ? (
            <Ga4ImportPreview
              preview={preview}
              pending={upload.isPending}
              onConfirm={() => {
                setProgress(0)
                upload.mutate()
              }}
              onCancel={resetPreview}
            />
          ) : (
            <Button
              className="self-start"
              disabled={!file || upload.isPending || dryRun.isPending}
              type="submit"
            >
              <UploadIcon data-icon="inline-start" />
              {previewsFirst(source) ? en.ga4Import.previewAction : 'Upload import'}
            </Button>
          )}
          {dryRun.isPending ? (
            <p role="status" className="text-sm text-muted-foreground">
              {en.ga4Import.previewing}
            </p>
          ) : null}
          {dryRun.error ? <p role="alert" className="text-sm text-destructive">{dryRun.error.message}</p> : null}
          {upload.isPending || progress > 0 ? (
            <p role="status" className="text-sm text-muted-foreground">
              Uploading {progress}%
            </p>
          ) : null}
          {upload.error ? <p role="alert" className="text-sm text-destructive">{upload.error.message}</p> : null}
        </form>

        {imports.isLoading ? <p className="text-sm text-muted-foreground">Loading imports…</p> : null}
        {imports.data?.length === 0 ? <p className="text-sm text-muted-foreground">No imports yet.</p> : null}
        {imports.data?.length ? (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead className="border-b text-muted-foreground">
                <tr>
                  <th className="py-2 pr-4 font-medium">File</th>
                  <th className="hidden py-2 pr-4 font-medium sm:table-cell">Source</th>
                  <th className="py-2 pr-4 font-medium">Status</th>
                  <th className="hidden py-2 pr-4 font-medium sm:table-cell">Rows</th>
                  <th className="hidden py-2 pr-4 font-medium sm:table-cell">Range</th>
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {imports.data.map((item) => (
                  <tr key={item.id} className="border-b last:border-0">
                    <td className="py-3 pr-4">
                      <p className="break-all font-medium">{item.file_name}</p>
                      <p className="text-xs text-muted-foreground tabular-nums sm:hidden">
                        {sourceLabels[item.source] ?? titleCase(item.source)}
                        {item.date_from
                          ? `, ${item.date_from.slice(0, 10)} – ${(item.date_to ?? item.date_from).slice(0, 10)}`
                          : ''}
                      </p>
                      {item.error ? <p className="text-xs text-destructive">{item.error}</p> : null}
                    </td>
                    <td className="hidden py-3 pr-4 sm:table-cell">{sourceLabels[item.source] ?? titleCase(item.source)}</td>
                    <td className="py-3 pr-4">{titleCase(item.status)}</td>
                    <td className="hidden py-3 pr-4 tabular-nums sm:table-cell">{fmt.number(item.rows)}</td>
                    <td className="hidden py-3 pr-4 tabular-nums sm:table-cell">
                      {item.date_from ? `${item.date_from.slice(0, 10)} – ${(item.date_to ?? item.date_from).slice(0, 10)}` : '—'}
                    </td>
                    <td className="py-3 text-right">
                      <Button
                        type="button"
                        variant="ghost"
                        className="min-h-11 min-w-11"
                        aria-label={`Undo import ${item.file_name}`}
                        disabled={remove.isPending}
                        onClick={() => setUndoTarget(item)}
                      >
                        <Undo2Icon data-icon="inline-start" />
                        {en.ga4Import.undo}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </Surface>

      <AlertDialog open={undoTarget !== undefined} onOpenChange={(open) => !open && setUndoTarget(undefined)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{en.ga4Import.undoTitle}</AlertDialogTitle>
            <AlertDialogDescription>
              {en.ga4Import.undoBody(undoTarget?.file_name ?? '')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{en.ga4Import.undoKeep}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (undoTarget) remove.mutate(undoTarget.id)
                setUndoTarget(undefined)
              }}
            >
              {en.ga4Import.undoConfirm}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {canExport ? <Surface title="Export site data" description="Download daily totals, dimensions, and custom events as CSV files in one ZIP.">
        <form className="flex flex-col gap-5" onSubmit={submitExport}>
          <FieldGroup className="sm:flex-row">
            <Field>
              <FieldLabel htmlFor="export-from">Export from</FieldLabel>
              <Input id="export-from" name="from" type="date" required defaultValue={today()} />
            </Field>
            <Field>
              <FieldLabel htmlFor="export-to">Export to</FieldLabel>
              <Input id="export-to" name="to" type="date" required defaultValue={today()} />
            </Field>
          </FieldGroup>
          <Button className="self-start" disabled={exportData.isPending} type="submit">
            <DownloadIcon data-icon="inline-start" />
            Download ZIP
          </Button>
          {exportData.error ? <p role="alert" className="text-sm text-destructive">{exportData.error.message}</p> : null}
        </form>
      </Surface> : null}
    </PageContainer>
  )
}
