import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { XIcon } from 'lucide-react'

import { Flag } from '@/components/Flag'
import type { Role } from '@/components/Sidebar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Switch } from '@/components/ui/switch'
import { countryLabel } from '@/lib/country'

import { privateRequest } from './privateApi'

type SiteExclusions = {
  id: number
  exclude_ips: string[]
  exclude_paths: string[]
  exclude_countries: string[]
  heatmaps_enabled: boolean
  respect_privacy_signals: boolean
}

const countryCodes =
  'AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW'.split(
    ' ',
  )

function lines(value: string): string[] {
  return value.split('\n').map((entry) => entry.trim()).filter(Boolean)
}

export function ExclusionsPage({
  siteId,
  orgId,
  role,
  canManageHeatmaps = false,
}: {
  siteId: number
  orgId: number
  role?: Role
  canManageHeatmaps?: boolean
}) {
  const queryClient = useQueryClient()
  const [ipRules, setIPRules] = useState<string | null>(null)
  const [pathRules, setPathRules] = useState<string | null>(null)
  const [countryRules, setCountryRules] = useState<string[] | null>(null)
  const [countrySearch, setCountrySearch] = useState('')
  const canManage = role === 'owner' || role === 'admin'
  const site = useQuery({
    queryKey: ['site', siteId],
    queryFn: () => privateRequest<SiteExclusions>('GET', `/api/private/sites/${siteId}`, { orgId }),
  })
  useEffect(() => {
    if (!site.data) return
    setIPRules(site.data.exclude_ips.join('\n'))
    setPathRules(site.data.exclude_paths.join('\n'))
    setCountryRules(site.data.exclude_countries ?? [])
  }, [site.data])

  const save = useMutation({
    mutationFn: (body: Pick<SiteExclusions, 'exclude_ips' | 'exclude_paths' | 'exclude_countries'>) =>
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
    save.mutate({
      exclude_ips: lines(ipRules ?? ''),
      exclude_paths: lines(pathRules ?? ''),
      exclude_countries: countryRules ?? [],
    })
  }

  const error = save.error ?? saveHeatmaps.error ?? savePrivacySignals.error ?? currentIP.error ?? site.error
  const message = error instanceof Error ? error.message : ''
  const ipInvalid = message.includes('exclude_ips')
  const pathInvalid = message.includes('exclude_paths')
  const countryInvalid = message.includes('exclude_countries')
  const countryMatches = useMemo(() => {
    const query = countrySearch.trim().toLocaleLowerCase()
    if (!query || !countryRules) return []
    const selected = new Set(countryRules)
    return countryCodes
      .filter((code) => !selected.has(code))
      .filter(
        (code) =>
          code.toLocaleLowerCase().includes(query) ||
          countryLabel(code).toLocaleLowerCase().includes(query),
      )
      .slice(0, 8)
  }, [countryRules, countrySearch])

  return (
    <PageContainer width="narrow">
      <PageHeader title="Exclusions" description="Ignore your own traffic and pages that should not appear in analytics." />
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
      <section className="flex flex-col gap-4 rounded-xl border bg-card p-4 md:p-5 text-card-foreground">
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
              {canManage && countryRules ? (
                <FieldSet aria-label="Blocked countries" data-invalid={countryInvalid || undefined}>
                  <FieldLegend variant="label">Blocked countries</FieldLegend>
                  <FieldDescription>
                    New events from these countries are ignored. Past analytics are unchanged. Maximum 50.
                  </FieldDescription>
                  {countryRules.length > 0 ? (
                    <div className="flex flex-wrap gap-2">
                      {countryRules.map((code) => (
                        <Badge key={code} variant="secondary">
                          <Flag code={code} />
                          {countryLabel(code)}
                          <Button
                            aria-label={`Remove ${countryLabel(code)}`}
                            size="icon-xs"
                            type="button"
                            variant="ghost"
                            onClick={() => setCountryRules((current) => current?.filter((entry) => entry !== code) ?? [])}
                          >
                            <XIcon aria-hidden="true" data-icon="inline-start" />
                          </Button>
                        </Badge>
                      ))}
                    </div>
                  ) : null}
                  <Field>
                    <FieldLabel htmlFor="country-search" className="sr-only">Find a country</FieldLabel>
                    <Input
                      id="country-search"
                      role="searchbox"
                      value={countrySearch}
                      placeholder="Find a country"
                      disabled={countryRules.length >= 50}
                      aria-invalid={countryInvalid || undefined}
                      onChange={(event) => setCountrySearch(event.target.value)}
                    />
                  </Field>
                  {countryMatches.length > 0 ? (
                    <div className="grid gap-1 rounded-lg border p-1">
                      {countryMatches.map((code) => (
                        <Button
                          key={code}
                          aria-label={`Add ${countryLabel(code)}`}
                          className="justify-start"
                          type="button"
                          variant="ghost"
                          onClick={() => {
                            setCountryRules((current) =>
                              current && current.length < 50 ? [...current, code] : current,
                            )
                            setCountrySearch('')
                          }}
                        >
                          <Flag code={code} />
                          {countryLabel(code)}
                        </Button>
                      ))}
                    </div>
                  ) : null}
                  <FieldError>{countryInvalid ? message : null}</FieldError>
                </FieldSet>
              ) : null}
            </FieldGroup>
            {message && !ipInvalid && !pathInvalid && !countryInvalid ? <FieldError>{message}</FieldError> : null}
            <Button className="self-start" type="submit" disabled={save.isPending}>
              {save.isPending ? 'Saving…' : 'Save exclusions'}
            </Button>
          </form>
        ) : (
          <p className="text-sm text-muted-foreground">Loading exclusions…</p>
        )}
      </section>
    </PageContainer>
  )
}
