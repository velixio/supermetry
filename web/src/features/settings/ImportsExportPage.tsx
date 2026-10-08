import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { DownloadIcon, Trash2Icon, UploadIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { fmt } from '@/lib/format'

import {
  downloadSiteExport,
  privateRequest,
  uploadSiteImport,
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
    <section className="flex flex-col gap-5 rounded-xl border bg-card p-5 text-card-foreground">
      <header className="flex flex-col gap-1">
        <h2 className="font-semibold">{title}</h2>
        <p className="text-sm text-muted-foreground">{description}</p>
      </header>
      {children}
    </section>
  )
}

export function ImportsExportPage({ siteId, orgId }: { siteId: number; orgId: number }) {
  const client = useQueryClient()
  const [source, setSource] = useState<ImportSource>('plausible')
  const [file, setFile] = useState<File>()
  const [progress, setProgress] = useState(0)
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
      return uploadSiteImport<SiteImport>(siteId, orgId, source, file, setProgress)
    },
    onSuccess: () => client.invalidateQueries({ queryKey: ['site-imports', siteId] }),
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
    upload.mutate()
  }

  function submitExport(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    exportData.mutate({
      from: String(data.get('from') ?? ''),
      to: String(data.get('to') ?? ''),
    })
  }

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">Imports &amp; export</h1>
        <p className="text-sm text-muted-foreground">
          Bring historical aggregate data into this site or download a portable ZIP.
        </p>
      </header>

      <Surface
        title="Import historical data"
        description="Plausible ZIP exports and Umami website-event CSVs are supported. Imports never replace native data."
      >
        <form className="flex flex-col gap-5" onSubmit={submitImport}>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="import-source">Import source</FieldLabel>
              <select
                id="import-source"
                className="h-9 rounded-lg border bg-background px-3 text-sm"
                value={source}
                onChange={(event) => setSource(event.target.value as ImportSource)}
              >
                <option value="plausible">Plausible</option>
                <option value="umami">Umami</option>
              </select>
            </Field>
            <Field>
              <FieldLabel htmlFor="import-file">Import file</FieldLabel>
              <Input
                id="import-file"
                type="file"
                accept={source === 'plausible' ? '.zip,application/zip' : '.csv,text/csv'}
                onChange={(event) => setFile(event.target.files?.[0])}
              />
              <FieldDescription>Maximum file size: 512 MB.</FieldDescription>
            </Field>
          </FieldGroup>
          <Button className="self-start" disabled={!file || upload.isPending} type="submit">
            <UploadIcon data-icon="inline-start" />
            Upload import
          </Button>
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
                  <th className="py-2 font-medium">File</th>
                  <th className="py-2 font-medium">Source</th>
                  <th className="py-2 font-medium">Status</th>
                  <th className="py-2 font-medium">Rows</th>
                  <th><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {imports.data.map((item) => (
                  <tr key={item.id} className="border-b last:border-0">
                    <td className="py-3 pr-4">
                      <p className="font-medium">{item.file_name}</p>
                      {item.error ? <p className="text-xs text-destructive">{item.error}</p> : null}
                    </td>
                    <td className="py-3 pr-4">{titleCase(item.source)}</td>
                    <td className="py-3 pr-4">{titleCase(item.status)}</td>
                    <td className="py-3 pr-4 tabular-nums">{fmt.number(item.rows)}</td>
                    <td className="py-3 text-right">
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        aria-label={`Delete ${item.file_name}`}
                        disabled={remove.isPending}
                        onClick={() => remove.mutate(item.id)}
                      >
                        <Trash2Icon />
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </Surface>

      <Surface title="Export site data" description="Download daily totals, dimensions, and custom events as CSV files in one ZIP.">
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
      </Surface>
    </div>
  )
}
