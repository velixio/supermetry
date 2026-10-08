import { useEffect, useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'
import { Switch } from '@/components/ui/switch'

import { privateRequest } from './privateApi'

type SiteExclusions = {
  id: number
  exclude_ips: string[]
  exclude_paths: string[]
  heatmaps_enabled: boolean
  respect_privacy_signals: boolean
}

function lines(value: string): string[] {
  return value.split('\n').map((entry) => entry.trim()).filter(Boolean)
}

export function ExclusionsPage({ siteId, orgId, canManageHeatmaps = false }: { siteId: number; orgId: number; canManageHeatmaps?: boolean }) {
  const queryClient = useQueryClient()
  const [ipRules, setIPRules] = useState<string | null>(null)
  const [pathRules, setPathRules] = useState<string | null>(null)
  const site = useQuery({
    queryKey: ['site', siteId],
    queryFn: () => privateRequest<SiteExclusions>('GET', `/api/private/sites/${siteId}`, { orgId }),
  })
  useEffect(() => {
    if (!site.data) return
    setIPRules(site.data.exclude_ips.join('\n'))
    setPathRules(site.data.exclude_paths.join('\n'))
  }, [site.data])

  const save = useMutation({
    mutationFn: (body: Pick<SiteExclusions, 'exclude_ips' | 'exclude_paths'>) =>
      privateRequest<SiteExclusions>('PATCH', `/api/private/sites/${siteId}`, { orgId, body }),
    onSuccess: (value) => queryClient.setQueryData(['site', siteId], value),
  })
  const currentIP = useMutation({
    mutationFn: () => privateRequest<{ ip: string }>('GET', '/api/private/me/ip'),
    onSuccess: ({ ip }) => {
      setIPRules((current) => {
        const existing = lines(current ?? '')
        const hostRule = `${ip}/${ip.includes(':') ? '128' : '32'}`
        if (existing.includes(ip) || existing.includes(hostRule)) return current ?? ''
        return [...existing, ip].join('\n')
      })
    },
  })
  const saveHeatmaps = useMutation({
    mutationFn: (enabled: boolean) =>
      privateRequest<SiteExclusions>('PATCH', `/api/private/sites/${siteId}`, {
        orgId,
        body: { heatmaps_enabled: enabled },
      }),
    onSuccess: (value) => {
      queryClient.setQueryData(['site', siteId], value)
      void queryClient.invalidateQueries({ queryKey: ['site-snippet', orgId, siteId] })
    },
  })
  const savePrivacySignals = useMutation({
    mutationFn: (enabled: boolean) =>
      privateRequest<SiteExclusions>('PATCH', `/api/private/sites/${siteId}`, {
        orgId,
        body: { respect_privacy_signals: enabled },
      }),
    onSuccess: (value) => queryClient.setQueryData(['site', siteId], value),
  })

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    save.mutate({ exclude_ips: lines(ipRules ?? ''), exclude_paths: lines(pathRules ?? '') })
  }

  const error = save.error ?? saveHeatmaps.error ?? savePrivacySignals.error ?? currentIP.error ?? site.error
  const message = error instanceof Error ? error.message : ''
  const ipInvalid = message.includes('exclude_ips')
  const pathInvalid = message.includes('exclude_paths')

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">Exclusions</h1>
        <p className="text-sm text-muted-foreground">
          Ignore your own traffic and pages that should not appear in analytics.
        </p>
      </header>
      <section className="flex items-center justify-between gap-4 rounded-xl border bg-card p-5 text-card-foreground">
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium" htmlFor="privacy-signals-enabled">Respect browser privacy signals</label>
          <p className="text-sm text-muted-foreground">
            Drop events when the browser sends Global Privacy Control or Do Not Track. Enabled by default.
          </p>
        </div>
        <Switch
          id="privacy-signals-enabled"
          checked={site.data?.respect_privacy_signals !== false}
          disabled={savePrivacySignals.isPending || !site.data}
          onCheckedChange={(checked) => savePrivacySignals.mutate(checked)}
        />
      </section>
      <section className="flex items-center justify-between gap-4 rounded-xl border bg-card p-5 text-card-foreground">
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium" htmlFor="heatmaps-enabled">Collect click heatmaps</label>
          <p className="text-sm text-muted-foreground">
            Uses the full tracker to collect anonymous coordinates only—no text, selectors, cookies, or identifiers.
          </p>
        </div>
        <Switch
          id="heatmaps-enabled"
          checked={site.data?.heatmaps_enabled === true}
          disabled={!canManageHeatmaps || saveHeatmaps.isPending || !site.data}
          onCheckedChange={(checked) => saveHeatmaps.mutate(checked)}
        />
      </section>
      <section className="flex flex-col gap-5 rounded-xl border bg-card p-5 text-card-foreground">
        {ipRules !== null && pathRules !== null ? (
          <form className="flex flex-col gap-5" onSubmit={submit}>
            <FieldGroup>
              <Field data-invalid={ipInvalid || undefined}>
                <FieldLabel htmlFor="exclude-ips">IP addresses and ranges</FieldLabel>
                <Textarea
                  id="exclude-ips"
                  value={ipRules}
                  onChange={(event) => setIPRules(event.target.value)}
                  placeholder={'203.0.113.7\n2001:db8::/32'}
                  aria-invalid={ipInvalid || undefined}
                />
                <FieldDescription>One IP address or CIDR range per line. Maximum 50.</FieldDescription>
                <FieldError>{ipInvalid ? message : null}</FieldError>
              </Field>
              <Button
                className="self-start"
                type="button"
                variant="outline"
                disabled={currentIP.isPending}
                onClick={() => currentIP.mutate()}
              >
                {currentIP.isPending ? 'Finding IP…' : 'Add my current IP'}
              </Button>
              <Field data-invalid={pathInvalid || undefined}>
                <FieldLabel htmlFor="exclude-paths">Page paths</FieldLabel>
                <Textarea
                  id="exclude-paths"
                  value={pathRules}
                  onChange={(event) => setPathRules(event.target.value)}
                  placeholder={'/admin/*\n/preview/**\n/internal'}
                  aria-invalid={pathInvalid || undefined}
                />
                <FieldDescription>
                  One path per line. * stays within one segment; ** crosses segments. Maximum 50.
                </FieldDescription>
                <FieldError>{pathInvalid ? message : null}</FieldError>
              </Field>
            </FieldGroup>
            {message && !ipInvalid && !pathInvalid ? <FieldError>{message}</FieldError> : null}
            <Button className="self-start" type="submit" disabled={save.isPending}>
              {save.isPending ? 'Saving…' : 'Save exclusions'}
            </Button>
          </form>
        ) : (
          <p className="text-sm text-muted-foreground">Loading exclusions…</p>
        )}
      </section>
    </div>
  )
}
