import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { fmt } from '@/lib/format'
import { uiSettings } from '@/messages/uiSettings'

import type { ApiKeyItem } from './keyStatus'

export function siteNames(key: ApiKeyItem, sites: { id: number; name: string }[]): string[] {
  return key.site_ids === null ? [] : key.site_ids.map((id) => sites.find((site) => site.id === id)?.name ?? `#${id}`)
}

export function KeyDetailsSheet({ apiKey, sites, onClose }: { apiKey: ApiKeyItem | null; sites: { id: number; name: string }[]; onClose: () => void }) {
  const t = uiSettings.keys
  const row = (label: string, value: React.ReactNode) => (
    <div className="flex flex-col gap-0.5">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-sm break-words">{value}</dd>
    </div>
  )
  return (
    <Sheet open={apiKey !== null} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent>
        {apiKey ? (
          <>
            <SheetHeader>
              <SheetTitle>{apiKey.name}</SheetTitle>
              <SheetDescription>{t.detailsTitle}</SheetDescription>
            </SheetHeader>
            <dl className="flex flex-col gap-4 px-4 pb-4">
              {row(t.detail.prefix, <code>{apiKey.prefix}</code>)}
              {row(t.detail.created, fmt.date(apiKey.created_at, 'UTC'))}
              {row(t.detail.expires, apiKey.expires_at ? fmt.date(apiKey.expires_at, 'UTC') : t.detail.never)}
              {row(t.detail.lastUsed, apiKey.last_used_at ? fmt.date(apiKey.last_used_at, 'UTC') : t.detail.never)}
              {row(t.detail.scopes, <span className="flex flex-wrap gap-1">{apiKey.scopes.map((scope) => <code key={scope} className="rounded bg-muted px-1.5 py-0.5 text-xs">{scope}</code>)}</span>)}
              {row(t.detail.sites, apiKey.site_ids === null ? t.allSites : siteNames(apiKey, sites).join(', '))}
            </dl>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}
