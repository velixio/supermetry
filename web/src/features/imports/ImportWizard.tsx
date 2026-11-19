import { useCallback, useEffect, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'

import { Button } from '@/components/ui/button'
import { useReauth } from '@/lib/useReauth'
import { importWizard } from '@/messages/importWizard'
import { previewSiteImport, uploadSiteImport } from '@/features/settings/privateApi'

import { importKeys, importsApi, isInFlight, type PullPreview, type SiteImport } from './api'
import {
  clearDraft, defaultDraft, loadDraft, previewRange, pullBody, rangeProblem, saveDraft, sourceReady,
  type Draft, type WizardStep,
} from './draft'
import { PreviewStep } from './PreviewStep'
import { RangeStep } from './RangeStep'
import { ReviewStep } from './ReviewStep'
import { RunStep } from './RunStep'
import { SourceStep } from './SourceStep'
import { WizardStepper } from './WizardStepper'

const t = importWizard
const MAX_FILE_BYTES = 512 * 1024 * 1024

export type WizardSearch = { ga4?: 'connected' | 'denied' | 'error'; import?: number }

type Props = {
  siteId: number
  orgId: number
  search?: WizardSearch
  /** Keeps the active import id in the URL so a reload returns to the run step. */
  onSearchChange?: (patch: WizardSearch) => void
  reviewTarget?: SiteImport
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback
}

export function ImportWizard({ siteId, orgId, search, onSearchChange, reviewTarget }: Props) {
  const { withReauth, reauthDialog } = useReauth()
  const [draft, setDraft] = useState<Draft>(() => {
    const base = defaultDraft()
    return search?.ga4 ? { ...base, ...loadDraft(siteId) } : base
  })
  const [step, setStep] = useState<WizardStep>(search?.import ? 'run' : 'source')
  const [importId, setImportId] = useState<number | undefined>(search?.import)
  const [uploadPercent, setUploadPercent] = useState<number>()
  const [finished, setFinished] = useState<SiteImport | undefined>(reviewTarget)
  const [partial, setPartial] = useState<{ shown: number; total: number }>()
  const [connectError, setConnectError] = useState<string>()
  const [ga4Outcome, setGa4Outcome] = useState(search?.ga4)

  const patch = useCallback((change: Partial<Draft>) => setDraft((current) => ({ ...current, ...change })), [])

  useEffect(() => {
    if (search?.ga4) {
      setGa4Outcome(search.ga4)
      onSearchChange?.({ ga4: undefined })
    }
    // Only react to the redirect once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // An external review request (from the history list) jumps to the review step.
  useEffect(() => {
    if (reviewTarget) {
      setFinished(reviewTarget)
      setImportId(reviewTarget.id)
      setStep('review')
    }
  }, [reviewTarget])

  // Pick up an import that is still running (page reload without the id in the URL).
  const list = useQuery({ queryKey: importKeys.list(siteId), queryFn: () => importsApi.list(siteId, orgId), enabled: step === 'source' && !search?.import })
  useEffect(() => {
    const running = list.data?.find((item) => isInFlight(item.status))
    if (running && step === 'source' && importId === undefined) {
      setImportId(running.id)
      setStep('run')
      onSearchChange?.({ import: running.id })
    }
  }, [list.data, step, importId, onSearchChange])

  const previewRun = useMutation<PullPreview>({
    mutationFn: async () => {
      if (draft.mode === 'file') {
        if (!draft.file) throw new Error(t.range.fileNone)
        if (draft.file.size > MAX_FILE_BYTES) throw new Error(t.preview.fileTooLarge)
        return (await previewSiteImport(siteId, orgId, draft.fileSource, draft.file)) as PullPreview
      }
      const range = previewRange(draft)
      setPartial(range.partial ? { shown: Math.min(range.total, 120), total: range.total } : undefined)
      return importsApi.previewPull(siteId, orgId, pullBody(draft, range))
    },
  })

  const start = useMutation<SiteImport>({
    mutationFn: async () => {
      if (draft.mode === 'file') {
        if (!draft.file) throw new Error(t.range.fileNone)
        setUploadPercent(0)
        setStep('run')
        return uploadSiteImport<SiteImport>(siteId, orgId, draft.fileSource, draft.file, setUploadPercent, undefined,
          previewRun.data?.overlap.same_file != null)
      }
      return withReauth(() => importsApi.createPull(siteId, orgId, pullBody(draft)))
    },
    onSuccess: (item) => {
      setImportId(item.id)
      setStep('run')
      onSearchChange?.({ import: item.id })
      clearDraft(siteId)
      // Credentials are no longer needed in the browser once the server holds them.
      patch({ apiKey: '', tokenAuth: '' })
    },
    onError: () => {
      if (draft.mode === 'file') setStep('preview')
    },
  })

  const connect = useMutation({
    mutationFn: () => withReauth(() => importsApi.connectGa4(siteId, orgId)),
    onSuccess: ({ authorization_url }) => {
      saveDraft(siteId, draft)
      window.location.assign(authorization_url)
    },
    onError: (error) => setConnectError(errorMessage(error, t.source.ga4ConnectFailed)),
  })

  function goPreview() {
    previewRun.reset()
    start.reset()
    setStep('preview')
    previewRun.mutate()
  }

  function startOver() {
    setStep('source')
    setImportId(undefined)
    setFinished(undefined)
    setUploadPercent(undefined)
    previewRun.reset()
    start.reset()
    setDraft(defaultDraft())
    onSearchChange?.({ import: undefined })
  }

  const rangeInvalid = draft.mode === 'api' && rangeProblem(draft) !== null

  return (
    <section className="flex flex-col gap-5 rounded-lg bg-card p-4 text-card-foreground md:p-5" aria-labelledby="import-wizard-title">
      <header className="flex flex-col gap-1">
        <h2 id="import-wizard-title" className="font-semibold">{t.title}</h2>
        <p className="text-sm text-muted-foreground">{t.intro}</p>
      </header>
      <WizardStepper current={step} />

      {step === 'source' ? (
        <SourceStep siteId={siteId} orgId={orgId} draft={draft} onChange={patch}
          onConnectGoogle={() => { setConnectError(undefined); connect.mutate() }}
          connecting={connect.isPending} connectError={connectError} ga4Outcome={ga4Outcome} />
      ) : null}
      {step === 'range' ? <RangeStep draft={draft} onChange={patch} /> : null}
      {step === 'preview' ? (
        <PreviewStep
          preview={previewRun.data} loading={previewRun.isPending}
          error={previewRun.isError ? errorMessage(previewRun.error, t.preview.failed) : undefined}
          partial={draft.mode === 'api' ? partial : undefined}
          starting={start.isPending} startError={start.isError ? errorMessage(start.error, t.preview.startFailed) : undefined}
          onRetry={goPreview} onStart={() => start.mutate()} onChangeRange={() => setStep('range')}
        />
      ) : null}
      {step === 'run' ? (
        <RunStep siteId={siteId} orgId={orgId} importId={importId} uploadPercent={uploadPercent}
          onFinished={(item) => { setFinished(item); setStep('review') }} onStartOver={startOver} />
      ) : null}
      {step === 'review' && finished ? (
        <ReviewStep siteId={siteId} orgId={orgId} item={finished} onUndone={startOver} onAnother={startOver} />
      ) : null}

      {step === 'source' || step === 'range' ? (
        <div className="flex flex-wrap gap-2">
          {step === 'range' ? <Button type="button" variant="outline" onClick={() => setStep('source')}>{t.back}</Button> : null}
          <Button type="button"
            disabled={step === 'source' ? !sourceReady(draft) : rangeInvalid}
            onClick={() => (step === 'source' ? setStep('range') : goPreview())}>
            {t.next}
          </Button>
        </div>
      ) : null}
      {reauthDialog}
    </section>
  )
}
