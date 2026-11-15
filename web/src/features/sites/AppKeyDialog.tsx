import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { copyText } from '@/lib/clipboard'
import { en } from '@/messages/en'

export function AppKeyDialog({ appId, ingestKey, onClose }: { appId: string; ingestKey: string; onClose: () => void }) {
  const snippet = `SUPERMETRY_APP_ID=${appId}\nSUPERMETRY_INGEST_KEY=${ingestKey}\n# header on every report: X-Supermetry-Key: ${ingestKey}`
  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>Your app's ingest key</DialogTitle>
          <DialogDescription>Shown once. Store it in your app's build configuration.</DialogDescription>
        </DialogHeader>
        <Field>
          <FieldLabel htmlFor="app-ingest-key">Ingest key</FieldLabel>
          <Input id="app-ingest-key" readOnly value={ingestKey} />
        </Field>
        <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs"><code>{snippet}</code></pre>
        <DialogFooter>
          <Button type="button" onClick={() => void copyText(ingestKey)}>Copy key</Button>
          <Button type="button" variant="outline" onClick={() => void copyText(snippet)}>Copy snippet</Button>
          <Button type="button" variant="outline" onClick={onClose}>{en.common.close}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
