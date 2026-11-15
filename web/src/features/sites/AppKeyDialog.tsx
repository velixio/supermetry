import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { TestReportCheck } from '@/features/apps/TestReportCheck'
import { copyText } from '@/lib/clipboard'
import { en } from '@/messages/en'

/** With `siteId`, the dialog also shows the setup checklist that waits for the first report. */
export function AppKeyDialog({ appId, ingestKey, siteId, timezone = 'UTC', onClose }: { appId: string; ingestKey: string; siteId?: number; timezone?: string; onClose: () => void }) {
  const snippet = `SUPERMETRY_APP_ID=${appId}\nSUPERMETRY_INGEST_KEY=${ingestKey}\n# header on every report: X-Supermetry-Key: ${ingestKey}`
  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent showCloseButton={false} className="max-h-[90vh] grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Your app's ingest key</DialogTitle>
          <DialogDescription>Shown once. Store it in your app's build configuration.</DialogDescription>
        </DialogHeader>
        <div data-slot="app-key-body" className="flex min-w-0 flex-col gap-4 overflow-y-auto">
        <Field>
          <FieldLabel htmlFor="app-ingest-key">Ingest key</FieldLabel>
          <Input id="app-ingest-key" readOnly value={ingestKey} />
        </Field>
        <pre className="whitespace-pre-wrap break-all rounded-lg bg-muted p-3 text-xs"><code>{snippet}</code></pre>
        {siteId ? <TestReportCheck appId={appId} siteId={siteId} timezone={timezone} /> : null}
        </div>
        <DialogFooter>
          <Button type="button" onClick={() => void copyText(ingestKey)}>Copy key</Button>
          <Button type="button" variant="outline" onClick={() => void copyText(snippet)}>Copy snippet</Button>
          <Button type="button" variant="outline" onClick={onClose}>{en.common.close}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
