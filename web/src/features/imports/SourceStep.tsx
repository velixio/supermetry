import { useEffect, useId, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'

import { SegmentedControl } from '@/components/SegmentedControl'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { EventExportFormat, eventExportAccept, isEventExportSource } from '@/features/settings/EventExportFormat'
import { Ga4Format } from '@/features/settings/Ga4ImportPreview'
import type { ImportSource } from '@/features/settings/privateApi'
import { importWizard } from '@/messages/importWizard'

import { importKeys, importsApi, type PullMethod } from './api'
import type { Draft } from './draft'

const t = importWizard.source

const fileSources: ImportSource[] = ['plausible', 'umami', 'ga4', 'mixpanel', 'amplitude']
const apiMethods: PullMethod[] = ['plausible_api', 'matomo_api', 'ga4_api']

function acceptFor(source: ImportSource): string {
  if (isEventExportSource(source)) return eventExportAccept[source]
  return source === 'plausible' ? '.zip,application/zip' : '.csv,text/csv'
}

function SelectField({ id, label, value, onChange, children, help }: {
  id: string; label: string; value: string | number; onChange: (value: string) => void; children: ReactNode; help?: string
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <select id={id} className="h-9 rounded-lg border bg-background px-3 text-sm" value={value} onChange={(event) => onChange(event.target.value)}>
        {children}
      </select>
      {help ? <FieldDescription>{help}</FieldDescription> : null}
    </Field>
  )
}

function TextField({ id, label, help, value, onChange, type = 'text', secret = false }: {
  id: string; label: string; help?: string; value: string; onChange: (value: string) => void; type?: string; secret?: boolean
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Input id={id} type={type} value={value} autoComplete="off" spellCheck={false}
        {...(secret ? { 'data-secret': 'true' } : {})} onChange={(event) => onChange(event.target.value)} />
      {help ? <FieldDescription>{help}</FieldDescription> : null}
    </Field>
  )
}

type Props = {
  siteId: number
  orgId: number
  draft: Draft
  onChange: (patch: Partial<Draft>) => void
  /** Starts the Google consent flow (wrapped in step-up by the caller). */
  onConnectGoogle: () => void
  connecting: boolean
  connectError?: string
  /** Outcome of the Google redirect, read from the URL. */
  ga4Outcome?: 'connected' | 'denied' | 'error'
}

export function SourceStep({ siteId, orgId, draft, onChange, onConnectGoogle, connecting, connectError, ga4Outcome }: Props) {
  const id = useId()
  const capabilities = useQuery({ queryKey: importKeys.capabilities(siteId), queryFn: () => importsApi.capabilities(siteId, orgId), enabled: draft.mode === 'api' })
  const connections = useQuery({
    queryKey: importKeys.connections(orgId), queryFn: () => importsApi.connections(orgId),
    enabled: draft.mode === 'api' && draft.apiMethod === 'ga4_api' && capabilities.data?.ga4_api.available === true,
  })
  const properties = useQuery({
    queryKey: importKeys.properties(orgId, draft.connectionId), queryFn: () => importsApi.properties(orgId, draft.connectionId),
    enabled: draft.mode === 'api' && draft.apiMethod === 'ga4_api' && draft.connectionId > 0,
  })
  const capability = capabilities.data?.[draft.apiMethod]
  const latestConnection = connections.data?.data.at(-1)?.id
  useEffect(() => {
    if (ga4Outcome === 'connected' && draft.connectionId === 0 && latestConnection) onChange({ connectionId: latestConnection })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ga4Outcome, latestConnection, draft.connectionId])
  const panel = `${id}-panel`

  function chooseMethod(method: PullMethod) {
    onChange({ apiMethod: method, baseUrl: method === 'plausible_api' ? 'https://plausible.io' : method === 'matomo_api' ? '' : draft.baseUrl })
  }

  return (
    <div className="flex flex-col gap-5" id={panel}>
      <h3 className="font-medium">{t.heading}</h3>
      <SegmentedControl
        aria-label={t.modeLabel} panelId={panel} value={draft.mode} onValueChange={(mode) => onChange({ mode })}
        options={[{ value: 'api', label: t.modeApi }, { value: 'file', label: t.modeFile }]}
      />

      {draft.mode === 'file' ? (
        <FieldGroup>
          <SelectField id={`${id}-file-source`} label={t.fileSourceLabel} value={draft.fileSource}
            onChange={(value) => onChange({ fileSource: value as ImportSource, file: undefined })}>
            {fileSources.map((source) => <option key={source} value={source}>{t.files[source]}</option>)}
          </SelectField>
          <Field>
            <FieldLabel htmlFor={`${id}-file`}>{t.fileLabel}</FieldLabel>
            <Input id={`${id}-file`} type="file" accept={acceptFor(draft.fileSource)}
              onChange={(event) => onChange({ file: event.target.files?.[0] })} />
            <FieldDescription>{t.fileHelp}</FieldDescription>
          </Field>
          {draft.fileSource === 'ga4' ? <Ga4Format /> : null}
          {isEventExportSource(draft.fileSource) ? <EventExportFormat source={draft.fileSource} /> : null}
        </FieldGroup>
      ) : (
        <FieldGroup>
          {capabilities.isLoading ? <p role="status" className="text-sm text-muted-foreground">{t.loadingCapabilities}</p> : null}
          {capabilities.isError ? <p role="alert" className="text-sm text-destructive">{t.capabilitiesFailed}</p> : null}
          <SelectField id={`${id}-api-source`} label={t.apiSourceLabel} value={draft.apiMethod} onChange={(value) => chooseMethod(value as PullMethod)}>
            {apiMethods.map((method) => (
              <option key={method} value={method} disabled={capabilities.data?.[method]?.available === false}>
                {t.apis[method]}
              </option>
            ))}
          </SelectField>
          {capability && !capability.available ? (
            <Alert><AlertDescription>{t.unavailable(capability.reason ?? '')}</AlertDescription></Alert>
          ) : null}

          {draft.apiMethod === 'plausible_api' ? (
            <>
              <TextField id={`${id}-base`} label={t.plausibleBaseUrl} help={t.plausibleBaseUrlHelp} value={draft.baseUrl} onChange={(baseUrl) => onChange({ baseUrl })} />
              <TextField id={`${id}-site`} label={t.plausibleSite} help={t.plausibleSiteHelp} value={draft.remoteSiteId} onChange={(remoteSiteId) => onChange({ remoteSiteId })} />
              <TextField id={`${id}-key`} label={t.plausibleKey} help={t.plausibleKeyHelp} type="password" secret value={draft.apiKey} onChange={(apiKey) => onChange({ apiKey })} />
              <p className="text-xs text-muted-foreground">{t.secretNote}</p>
            </>
          ) : null}
          {draft.apiMethod === 'matomo_api' ? (
            <>
              <TextField id={`${id}-base`} label={t.matomoBaseUrl} help={t.matomoBaseUrlHelp} value={draft.baseUrl} onChange={(baseUrl) => onChange({ baseUrl })} />
              <TextField id={`${id}-site`} label={t.matomoSite} help={t.matomoSiteHelp} value={draft.remoteSiteId} onChange={(remoteSiteId) => onChange({ remoteSiteId })} />
              <TextField id={`${id}-token`} label={t.matomoToken} help={t.matomoTokenHelp} type="password" secret value={draft.tokenAuth} onChange={(tokenAuth) => onChange({ tokenAuth })} />
              <p className="text-xs text-muted-foreground">{t.secretNote}</p>
            </>
          ) : null}
          {draft.apiMethod === 'ga4_api' && capability?.available ? (
            <section aria-labelledby={`${id}-ga4`} className="flex flex-col gap-4 rounded-lg border bg-muted/40 p-4">
              <h4 id={`${id}-ga4`} className="font-medium">{t.ga4Heading}</h4>
              {ga4Outcome === 'connected' ? <p role="status" className="text-sm text-success">{t.ga4Connected}</p> : null}
              {ga4Outcome === 'denied' ? <p role="alert" className="text-sm text-destructive">{t.ga4Denied}</p> : null}
              {ga4Outcome === 'error' ? <p role="alert" className="text-sm text-destructive">{t.ga4Error}</p> : null}
              {connections.data && connections.data.data.length > 0 ? (
                <SelectField id={`${id}-conn`} label={t.ga4Connection} value={draft.connectionId || ''}
                  onChange={(value) => onChange({ connectionId: Number(value), property: '' })}>
                  <option value="">{t.ga4Choose}</option>
                  {connections.data.data.map((connection) => (
                    <option key={connection.id} value={connection.id}>{t.ga4ConnectionOption(connection.id, connection.created_at.slice(0, 10))}</option>
                  ))}
                </SelectField>
              ) : <p className="text-sm text-muted-foreground">{t.ga4ConnectHelp}</p>}
              <Button type="button" variant="outline" className="self-start" disabled={connecting} onClick={onConnectGoogle}>
                {connecting ? t.ga4Connecting : connections.data?.data.length ? t.ga4ConnectAnother : t.ga4Connect}
              </Button>
              {connectError ? <p role="alert" className="text-sm text-destructive">{connectError}</p> : null}
              {draft.connectionId > 0 ? (
                properties.isLoading ? <p role="status" className="text-sm text-muted-foreground">{t.ga4PropertiesLoading}</p>
                : properties.isError ? <p role="alert" className="text-sm text-destructive">{t.ga4PropertiesFailed}</p>
                : properties.data && properties.data.data.length === 0 ? <p className="text-sm text-muted-foreground">{t.ga4PropertiesEmpty}</p>
                : properties.data ? (
                  <SelectField id={`${id}-prop`} label={t.ga4Property} value={draft.property} onChange={(property) => onChange({ property })}>
                    <option value="">{t.ga4Choose}</option>
                    {properties.data.data.map((item) => (
                      <option key={item.property} value={item.property}>{t.ga4PropertyOption(item.display_name, item.property)}</option>
                    ))}
                  </SelectField>
                ) : null
              ) : null}
            </section>
          ) : null}
        </FieldGroup>
      )}
    </div>
  )
}
