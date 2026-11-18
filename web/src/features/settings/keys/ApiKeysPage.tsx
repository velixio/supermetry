import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRoundIcon, MoreHorizontalIcon } from 'lucide-react'

import { ConfirmDangerDialog } from '@/components/ConfirmDangerDialog'
import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { StatusChip } from '@/components/StatusChip'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { fmt } from '@/lib/format'
import { activeLocale } from '@/lib/i18n/intl'
import { useReauth } from '@/lib/useReauth'
import { uiSettings } from '@/messages/uiSettings'

import { privateRequest, type ListResponse } from '../privateApi'
import { CreateKeyWizard } from './CreateKeyWizard'
import { KeyDetailsSheet } from './KeyDetailsSheet'
import { isUnused, keyStatus, relativeTime, statusVariant, type ApiKeyItem } from './keyStatus'
import type { Purpose } from './presets'
import { RollKeyDialog } from './RollKeyDialog'

type Role = 'owner' | 'admin' | 'editor' | 'viewer'
type SiteRef = { id: number; name: string }

export function ApiKeysPage({ orgId, role, sites, createRequested = false, onCreateClosed, initialPurpose }: {
  orgId: number
  /** Kept for callers that still pass the slug. */
  orgSlug?: string
  role: Role
  sites: SiteRef[]
  /** ?create=1: open the wizard on mount. */
  createRequested?: boolean
  onCreateClosed?: () => void
  initialPurpose?: Purpose
}) {
  const t = uiSettings.keys
  if (role !== 'owner' && role !== 'admin') {
    return <p role="alert" className="text-sm text-muted-foreground">{t.unauthorized}</p>
  }
  return <KeysManager orgId={orgId} sites={sites} createRequested={createRequested} onCreateClosed={onCreateClosed} initialPurpose={initialPurpose} />
}

function accessSummary(key: ApiKeyItem) {
  const t = uiSettings.keys
  const shown = key.scopes.slice(0, 2)
  const extra = key.scopes.length - shown.length
  return (
    <div className="flex flex-col gap-1">
      <div className="flex flex-wrap items-center gap-1">
        {shown.map((scope) => <Badge variant="outline" key={scope}>{scope}</Badge>)}
        {extra > 0 ? <span className="text-xs text-muted-foreground">{t.scopesMore(extra)}</span> : null}
      </div>
      <span className="text-xs text-muted-foreground">{key.site_ids === null ? t.allSites : t.sitesCount(key.site_ids.length)}</span>
    </div>
  )
}

function LastUsed({ apiKey }: { apiKey: ApiKeyItem }) {
  const t = uiSettings.keys
  if (!apiKey.last_used_at) return <span className="text-muted-foreground">{t.neverUsed}</span>
  const parts = [fmt.date(apiKey.last_used_at, 'UTC')]
  if (apiKey.last_used_client) parts.push(`${t.lastUsedTooltip.client}: ${t.clients[apiKey.last_used_client]}`)
  if (apiKey.last_used_country) parts.push(`${t.lastUsedTooltip.country}: ${apiKey.last_used_country}`)
  const detail = parts.join(' · ')
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span tabIndex={0} className="cursor-default rounded-sm underline decoration-dotted underline-offset-4 outline-none focus-visible:ring-2 focus-visible:ring-ring">
          {relativeTime(apiKey.last_used_at, activeLocale())}
          <span className="sr-only"> ({detail})</span>
        </span>
      </TooltipTrigger>
      <TooltipContent>{detail}</TooltipContent>
    </Tooltip>
  )
}

function KeysManager({ orgId, sites, createRequested, onCreateClosed, initialPurpose }: {
  orgId: number; sites: SiteRef[]; createRequested: boolean; onCreateClosed?: () => void; initialPurpose?: Purpose
}) {
  const t = uiSettings.keys
  const queryClient = useQueryClient()
  const { withReauth, reauthDialog } = useReauth()
  const [createOpen, setCreateOpen] = useState(createRequested)
  const [details, setDetails] = useState<ApiKeyItem | null>(null)
  const [rolling, setRolling] = useState<ApiKeyItem | null>(null)
  const [revoking, setRevoking] = useState<ApiKeyItem | null>(null)
  const [revokeError, setRevokeError] = useState('')
  const queryKey = ['api-keys', orgId]
  const keys = useQuery({
    queryKey,
    queryFn: () => privateRequest<ListResponse<ApiKeyItem>>('GET', `/api/private/orgs/${orgId}/api-keys`, { orgId }),
  })
  const revoke = useMutation({
    mutationFn: (id: number) => privateRequest<void>('DELETE', `/api/private/orgs/${orgId}/api-keys/${id}`, { orgId }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey }),
  })
  const now = Date.now()
  const list = keys.data?.data ?? []

  return (
    <PageContainer className="max-w-[1200px]">
      <PageHeader
        title={t.title}
        description={t.description}
        actions={
          <Button type="button" onClick={() => setCreateOpen(true)}>
            <KeyRoundIcon data-icon="inline-start" />
            {t.create}
          </Button>
        }
      />
      {keys.error ? <p role="alert" className="text-sm text-destructive">{keys.error instanceof Error ? keys.error.message : String(keys.error)}</p> : null}
      {revokeError ? <p role="alert" className="text-sm text-destructive">{revokeError}</p> : null}
      {keys.isPending ? <p role="status" className="text-sm text-muted-foreground">{t.loading}</p> : null}
      {keys.isSuccess && list.length === 0 ? <p className="text-sm text-muted-foreground">{t.empty}</p> : null}
      {list.length > 0 ? (
        <TooltipProvider>
        <div className="rounded-lg border bg-card text-card-foreground">
          <Table aria-label={t.tableLabel}>
            <TableHeader>
              <TableRow>
                <TableHead>{t.columns.name}</TableHead>
                <TableHead>{t.columns.access}</TableHead>
                <TableHead>{t.columns.lastUsed}</TableHead>
                <TableHead>{t.columns.expires}</TableHead>
                <TableHead className="w-12"><span className="sr-only">{t.columns.actions}</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((apiKey) => {
                const status = keyStatus(apiKey, now)
                return (
                  <TableRow key={apiKey.id}>
                    <TableCell className="font-medium">{apiKey.name}</TableCell>
                    <TableCell>{accessSummary(apiKey)}</TableCell>
                    <TableCell>
                      <div className="flex flex-wrap items-center gap-2">
                        <LastUsed apiKey={apiKey} />
                        {isUnused(apiKey, now) ? <StatusChip variant="neutral">{t.unused}</StatusChip> : null}
                      </div>
                    </TableCell>
                    <TableCell><StatusChip variant={statusVariant[status]} data-status={status}>{t.status[status]}</StatusChip></TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button type="button" variant="ghost" size="icon" aria-label={t.rowMenu(apiKey.name)}>
                            <MoreHorizontalIcon aria-hidden="true" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onSelect={() => setDetails(apiKey)}>{t.details}</DropdownMenuItem>
                          <DropdownMenuItem disabled={status === 'revoked'} onSelect={() => setRolling(apiKey)}>{t.roll}</DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem variant="destructive" disabled={status === 'revoked'} onSelect={() => { setRevokeError(''); setRevoking(apiKey) }}>{t.revoke}</DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
        </TooltipProvider>
      ) : null}

      <CreateKeyWizard
        open={createOpen}
        onOpenChange={(open) => { setCreateOpen(open); if (!open) onCreateClosed?.() }}
        orgId={orgId}
        sites={sites}
        initialPurpose={initialPurpose}
      />
      <KeyDetailsSheet apiKey={details} sites={sites} onClose={() => setDetails(null)} />
      <RollKeyDialog orgId={orgId} apiKey={rolling} onClose={() => setRolling(null)} />
      <ConfirmDangerDialog
        trigger={<span hidden />}
        open={revoking !== null}
        onOpenChange={(open) => { if (!open) setRevoking(null) }}
        title={t.revokeTitle(revoking?.name ?? '')}
        description={t.revokeBody(revoking?.name ?? '')}
        confirmLabel={t.revokeConfirm}
        onConfirm={async () => {
          if (!revoking) return
          try {
            await withReauth(() => revoke.mutateAsync(revoking.id))
          } catch (failure) {
            setRevokeError(failure instanceof Error ? failure.message : String(failure))
            throw failure
          }
        }}
      />
      {reauthDialog}
    </PageContainer>
  )
}
