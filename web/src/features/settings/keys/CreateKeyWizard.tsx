import { useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { AlertTriangleIcon, CheckIcon, CopyIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldContent, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useCopyStatus } from '@/features/settings/InstallRecipes'
import { privateRequest } from '@/features/settings/privateApi'
import { useReauth } from '@/lib/useReauth'
import { en } from '@/messages/en'
import { uiSettings } from '@/messages/uiSettings'

import { CopyBlock } from './CopyBlock'
import { McpClientConfig } from './McpClientConfig'
import { apiKeyScopes, DEFAULT_EXPIRY, expiryFields, purposePresets, type CreateKeyBody, type ExpiryChoice, type Purpose } from './presets'

type SiteRef = { id: number; name: string }
type Created = { id: number; key: string; expires_at: string | null }

const purposes: Purpose[] = ['stats', 'mcp', 'ci']
const expiryChoices: ExpiryChoice[] = ['30', '90', '180', '365', 'custom', 'never']

export function CreateKeyWizard({ open, onOpenChange, orgId, sites, initialPurpose }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  orgId: number
  sites: SiteRef[]
  /** When set the wizard opens on the Access step with this purpose selected. */
  initialPurpose?: Purpose
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {open ? <WizardBody orgId={orgId} sites={sites} initialPurpose={initialPurpose} onClose={() => onOpenChange(false)} /> : null}
    </Dialog>
  )
}

function WizardBody({ orgId, sites, initialPurpose, onClose }: { orgId: number; sites: SiteRef[]; initialPurpose?: Purpose; onClose: () => void }) {
  const t = uiSettings.wizard
  const queryClient = useQueryClient()
  const { withReauth, reauthDialog } = useReauth()
  const [step, setStep] = useState<0 | 1 | 2>(initialPurpose ? 1 : 0)
  const [purpose, setPurpose] = useState<Purpose>(initialPurpose ?? 'stats')
  const [name, setName] = useState(t.purposes[initialPurpose ?? 'stats'].name)
  const [scopes, setScopes] = useState<string[]>(purposePresets[initialPurpose ?? 'stats'].scopes)
  const [siteMode, setSiteMode] = useState<'all' | 'selected'>('all')
  const [siteIds, setSiteIds] = useState<number[]>([])
  const [expiry, setExpiry] = useState<ExpiryChoice>(DEFAULT_EXPIRY)
  const [customDate, setCustomDate] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<Created | null>(null)
  const { copied, copy } = useCopyStatus()

  function choosePurpose(next: Purpose) {
    setPurpose(next)
    setScopes(purposePresets[next].scopes)
    setName(t.purposes[next].name)
  }

  function validate(): string {
    if (scopes.length === 0) return t.scopesRequired
    if (siteMode === 'selected' && siteIds.length === 0) return t.sitesRequired
    if (expiry === 'custom') {
      const today = new Date().toISOString().slice(0, 10)
      if (!customDate || customDate <= today) return t.expiryFuture
    }
    return ''
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    const problem = validate()
    setError(problem)
    if (problem) return
    const body: CreateKeyBody = {
      name: name.trim(),
      scopes,
      site_ids: siteMode === 'all' ? null : siteIds,
      ...expiryFields(expiry, customDate),
    }
    setBusy(true)
    try {
      const result = await withReauth(() => privateRequest<Created>('POST', `/api/private/orgs/${orgId}/api-keys`, { orgId, body }))
      setCreated(result)
      setStep(2)
      void queryClient.invalidateQueries({ queryKey: ['api-keys', orgId] })
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure))
    } finally {
      setBusy(false)
    }
  }

  function close() {
    setCreated(null)
    onClose()
  }

  const secret = created?.key ?? ''
  const curl = `curl ${window.location.origin}/api/public/v1/sites \\\n  -H "Authorization: Bearer ${secret}"`

  return (
    <>
      <DialogContent
        className="max-h-[90vh] overflow-y-auto sm:max-w-2xl"
        // The secret is shown once: a stray click outside must not dismiss it.
        onInteractOutside={(event) => { if (step === 2) event.preventDefault() }}
        onEscapeKeyDown={(event) => { if (step === 2) event.preventDefault() }}
        showCloseButton={step !== 2}
      >
        <DialogHeader>
          <DialogTitle>{step === 2 ? t.done : t.title}</DialogTitle>
          <DialogDescription>{step === 2 ? t.doneDescription : t.stepOf(step + 1, t.steps.length, t.steps[step])}</DialogDescription>
        </DialogHeader>

        {step === 0 ? (
          <form className="flex flex-col gap-5" onSubmit={(event) => { event.preventDefault(); setStep(1) }}>
            <FieldSet>
              <FieldLegend>{t.purposeQuestion}</FieldLegend>
              <RadioGroup value={purpose} onValueChange={(value) => choosePurpose(value as Purpose)} className="gap-3">
                {purposes.map((value) => (
                  <Field orientation="horizontal" key={value}>
                    <RadioGroupItem value={value} id={`purpose-${value}`} />
                    <FieldContent>
                      <FieldLabel htmlFor={`purpose-${value}`}>{t.purposes[value].label}</FieldLabel>
                      <FieldDescription>{t.purposes[value].hint}</FieldDescription>
                    </FieldContent>
                  </Field>
                ))}
              </RadioGroup>
            </FieldSet>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={close}>{uiSettings.keys.cancel}</Button>
              <Button type="submit">{t.next}</Button>
            </DialogFooter>
          </form>
        ) : null}

        {step === 1 ? (
          <form className="flex flex-col gap-5" onSubmit={(event) => void submit(event)}>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="wizard-key-name">{t.name}</FieldLabel>
                <Input id="wizard-key-name" value={name} placeholder={t.namePlaceholder} required onChange={(event) => setName(event.target.value)} />
              </Field>
              <FieldSet>
                <FieldLegend>{t.whoCan}</FieldLegend>
                <FieldDescription>{t.scopeHelp}</FieldDescription>
                <FieldGroup data-slot="checkbox-group" className="gap-3">
                  {apiKeyScopes.map((scope) => (
                    <Field orientation="horizontal" key={scope}>
                      <Checkbox
                        id={`wizard-scope-${scope}`}
                        checked={scopes.includes(scope)}
                        onCheckedChange={(checked) => setScopes((current) => (checked === true ? [...current, scope] : current.filter((value) => value !== scope)))}
                      />
                      <FieldContent>
                        <FieldLabel htmlFor={`wizard-scope-${scope}`}>
                          {en.apiKeys.scopeLabels[scope]} <code className="text-xs font-normal text-muted-foreground">{scope}</code>
                        </FieldLabel>
                        <FieldDescription>{en.apiKeys.scopeDescriptions[scope]}</FieldDescription>
                      </FieldContent>
                    </Field>
                  ))}
                </FieldGroup>
              </FieldSet>
              <FieldSet>
                <FieldLegend>{t.sitesLegend}</FieldLegend>
                <RadioGroup value={siteMode} onValueChange={(value) => setSiteMode(value as 'all' | 'selected')} className="gap-2">
                  <Field orientation="horizontal"><RadioGroupItem value="all" id="wizard-sites-all" /><FieldLabel htmlFor="wizard-sites-all">{t.allSites}</FieldLabel></Field>
                  <Field orientation="horizontal"><RadioGroupItem value="selected" id="wizard-sites-selected" /><FieldLabel htmlFor="wizard-sites-selected">{t.onlySites}</FieldLabel></Field>
                </RadioGroup>
                {siteMode === 'selected' ? (
                  <FieldGroup data-slot="checkbox-group" className="gap-2 pl-6">
                    {sites.map((site) => (
                      <Field orientation="horizontal" key={site.id}>
                        <Checkbox
                          id={`wizard-site-${site.id}`}
                          checked={siteIds.includes(site.id)}
                          onCheckedChange={(checked) => setSiteIds((current) => (checked === true ? [...current, site.id] : current.filter((id) => id !== site.id)))}
                        />
                        <FieldLabel htmlFor={`wizard-site-${site.id}`}>{site.name}</FieldLabel>
                      </Field>
                    ))}
                  </FieldGroup>
                ) : null}
              </FieldSet>
              <Field>
                <FieldLabel htmlFor="wizard-expiry">{t.expiry}</FieldLabel>
                <Select value={expiry} onValueChange={(value) => setExpiry(value as ExpiryChoice)}>
                  <SelectTrigger id="wizard-expiry" className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {expiryChoices.map((choice) => <SelectItem key={choice} value={choice}>{t.expiryOptions[choice]}</SelectItem>)}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </Field>
              {expiry === 'custom' ? (
                <Field>
                  <FieldLabel htmlFor="wizard-expiry-date">{t.customDate}</FieldLabel>
                  <Input id="wizard-expiry-date" type="date" value={customDate} onChange={(event) => setCustomDate(event.target.value)} />
                </Field>
              ) : null}
              {expiry === 'never' ? (
                <p role="note" className="flex items-start gap-2 rounded-lg bg-warning/10 p-3 text-sm text-warning">
                  <AlertTriangleIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
                  <span>{t.noExpiryWarning}</span>
                </p>
              ) : null}
            </FieldGroup>
            {error ? <FieldError>{error}</FieldError> : null}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setStep(0)}>{t.back}</Button>
              <Button type="submit" disabled={busy || name.trim() === ''} aria-busy={busy}>{busy ? t.creating : t.createKey}</Button>
            </DialogFooter>
          </form>
        ) : null}

        {step === 2 && created ? (
          <div className="flex flex-col gap-5">
            <div role="alert" className="rounded-lg bg-warning/10 p-3 text-sm">
              <p className="font-medium text-warning">{t.wontSeeAgain}</p>
              <p className="text-muted-foreground">{t.wontSeeAgainBody}</p>
            </div>
            <Field>
              <FieldLabel htmlFor="created-api-key">{t.secretLabel}</FieldLabel>
              <div className="flex gap-2">
                <Input id="created-api-key" readOnly value={secret} className="font-mono text-xs" onFocus={(event) => event.currentTarget.select()} />
                <Button type="button" onClick={() => void copy(secret)}>
                  {copied === 'yes' ? <CheckIcon data-icon="inline-start" /> : <CopyIcon data-icon="inline-start" />}
                  {t.copyKey}
                </Button>
              </div>
              <span role="status" aria-live="polite" className="text-sm text-muted-foreground">{copied === 'yes' ? t.copied : copied === 'manual' ? uiSettings.mcp.copyFailed : ''}</span>
            </Field>
            {purpose === 'mcp' ? (
              <section className="flex flex-col gap-2" aria-label={t.mcpConfigTitle}>
                <h3 className="text-sm font-medium">{t.mcpConfigTitle}</h3>
                <McpClientConfig apiKey={secret} />
              </section>
            ) : (
              <section className="flex flex-col gap-2" aria-label={t.curlTitle}>
                <h3 className="text-sm font-medium">{t.curlTitle}</h3>
                <CopyBlock value={curl} what="curl command" />
              </section>
            )}
            <DialogFooter>
              <Button type="button" onClick={close}>{t.finish}</Button>
            </DialogFooter>
          </div>
        ) : null}
      </DialogContent>
      {reauthDialog}
    </>
  )
}
