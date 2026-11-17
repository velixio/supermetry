import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'

import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { CopyButton } from '@/features/settings/InstallRecipes'
import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

export type DsnKey = components['schemas']['AppDSNKey']

const queryKey = (siteId: number) => ['app-dsn-keys', siteId]
const path = (siteId: number) => `/api/private/sites/${siteId}/app/dsn-keys`

/** The server builds the DSN from its public base URL; fall back to this page's origin when it has none. */
export function dsnOf(key: DsnKey, siteId: number): string {
  if (key.dsn) return key.dsn
  const url = new URL(window.location.origin)
  return `${url.protocol}//${key.public_key}@${url.host}/${siteId}`
}

export function useDsnKeys(orgId: number, siteId: number) {
  return useQuery({ queryKey: queryKey(siteId), queryFn: () => privateRequest<{ keys: DsnKey[] }>('GET', path(siteId), { orgId }) })
}

function parseOrigins(text: string): string[] {
  return text.split(/[\s,]+/).map((origin) => origin.trim()).filter(Boolean)
}

/** DSN keys: create with optional allowed origins, revoke. The key is public by design, so it is always shown in full. */
export function DsnKeys({ orgId, siteId, keys }: { orgId: number; siteId: number; keys: DsnKey[] }) {
  const client = useQueryClient()
  const [label, setLabel] = useState('')
  const [origins, setOrigins] = useState('')
  const refresh = () => client.invalidateQueries({ queryKey: queryKey(siteId) })
  const create = useMutation({
    mutationFn: () => privateRequest<DsnKey>('POST', path(siteId), { orgId, body: { label: label.trim(), allowed_origins: parseOrigins(origins) } }),
    onSuccess: () => { setLabel(''); setOrigins(''); return refresh() },
  })
  const revoke = useMutation({
    mutationFn: (prefix: string) => privateRequest<void>('DELETE', `${path(siteId)}/${prefix}`, { orgId }),
    onSuccess: refresh,
  })
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {keys.length > 0 ? (
        <ul className="flex min-w-0 flex-col gap-3" aria-label="DSN keys">
          {keys.map((key) => (
            <li key={key.public_key} className="flex min-w-0 flex-col gap-2 rounded-lg border p-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="text-sm font-medium">{key.label || `Key ${key.prefix}`}</span>
                <AlertDialog>
                  <AlertDialogTrigger asChild><Button type="button" variant="outline" size="sm" aria-label={`Revoke key ${key.prefix}`}>Revoke</Button></AlertDialogTrigger>
                  <AlertDialogContent>
                    <AlertDialogHeader>
                      <AlertDialogTitle>Revoke this DSN key?</AlertDialogTitle>
                      <AlertDialogDescription>Apps using it stop reporting at once. Key {key.prefix}… cannot be restored.</AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                      <AlertDialogCancel>Cancel</AlertDialogCancel>
                      <AlertDialogAction onClick={() => revoke.mutate(key.prefix)}>Revoke key</AlertDialogAction>
                    </AlertDialogFooter>
                  </AlertDialogContent>
                </AlertDialog>
              </div>
              <code className="min-w-0 break-all rounded-md bg-muted px-3 py-2 font-mono text-xs select-all" aria-label={`DSN ${key.prefix}`}>{dsnOf(key, siteId)}</code>
              <p className="text-xs text-muted-foreground">
                {key.allowed_origins.length > 0 ? <>Allowed origins: {key.allowed_origins.join(', ')}</> : 'Any origin may send with this key.'}
                {key.expires_at ? <> · expires {new Date(key.expires_at).toLocaleDateString()}</> : null}
              </p>
            </li>
          ))}
        </ul>
      ) : null}
      {revoke.isError ? <p role="alert" className="text-sm text-destructive">{revoke.error.message}</p> : null}
      <form
        className="flex min-w-0 flex-col gap-3 rounded-lg border border-dashed p-3"
        onSubmit={(event) => { event.preventDefault(); create.mutate() }}
      >
        <h3 className="text-sm font-medium">Create a DSN key</h3>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="dsn-label">Label (optional)</Label>
          <Input id="dsn-label" value={label} maxLength={80} onChange={(event) => setLabel(event.target.value)} placeholder="Production web" />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="dsn-origins">Allowed origins (optional)</Label>
          <Textarea id="dsn-origins" value={origins} onChange={(event) => setOrigins(event.target.value)} placeholder="https://app.example.com" rows={2} />
          <p className="text-xs text-muted-foreground">One scheme://host[:port] per line. Browsers from other origins are refused. Leave empty for apps and servers.</p>
        </div>
        {create.isError ? <p role="alert" className="text-sm text-destructive">{create.error.message}</p> : null}
        <div><Button type="submit" disabled={create.isPending}>Create key</Button></div>
      </form>
    </div>
  )
}

export function PrimaryDsn({ dsn }: { dsn: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <code className="min-w-0 break-all rounded-md bg-muted px-3 py-2 font-mono text-sm select-all" aria-label="DSN">{dsn}</code>
      <CopyButton value={dsn} label="Copy DSN" />
    </div>
  )
}
