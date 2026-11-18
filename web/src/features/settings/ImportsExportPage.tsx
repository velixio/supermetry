import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useCapability } from '@/lib/authorization'
import { useState, type FormEvent } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { DownloadIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { importKeys, importsApi, pollWhileInFlight, type SiteImport as WizardImport } from '@/features/imports/api'
import { ImportHistory } from '@/features/imports/ImportHistory'
import { ImportWizard, type WizardSearch } from '@/features/imports/ImportWizard'
import { SwitchOverPanel } from '@/features/imports/SwitchOverPanel'

import { downloadSiteExport } from './privateApi'

export type SiteImport = WizardImport

export function importPollInterval(imports: Array<{ status: string }>): 2_000 | false {
  return imports.some((item) => pollWhileInFlight(item) !== false) ? 2_000 : false
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

type PageProps = {
  siteId: number
  orgId: number
  search?: WizardSearch
  onSearchChange?: (patch: WizardSearch) => void
}

export function ImportsExportPage({ siteId, orgId, search, onSearchChange }: PageProps) {
  const canExport = useCapability('data.export')
  const [reviewTarget, setReviewTarget] = useState<SiteImport>()
  const imports = useQuery({
    queryKey: importKeys.list(siteId),
    queryFn: () => importsApi.list(siteId, orgId),
    refetchInterval: (query) => importPollInterval(query.state.data ?? []),
  })
  const exportData = useMutation({
    mutationFn: ({ from, to }: { from: string; to: string }) =>
      downloadSiteExport(siteId, orgId, from, to),
  })

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

      <ImportWizard siteId={siteId} orgId={orgId} search={search} onSearchChange={onSearchChange} reviewTarget={reviewTarget} />
      <div className="rounded-xl border bg-card p-4 text-card-foreground md:p-5">
        <ImportHistory siteId={siteId} orgId={orgId} items={imports.data} loading={imports.isLoading} onReview={setReviewTarget} />
      </div>
      <div className="rounded-xl border bg-card p-4 text-card-foreground md:p-5">
        <SwitchOverPanel siteId={siteId} orgId={orgId} />
      </div>

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
