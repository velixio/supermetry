import { GeolocationCredit } from '@/components/GeolocationCredit'
import { PoweredBySupermetry } from '@/components/PoweredBySupermetry'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { versionLabel } from '@/lib/version'
import { ui } from '@/messages/ui'

/**
 * Help > About: the build version and the credits that used to sit in the page footer.
 * The geolocation credit (MaxMind's licence asks for attribution wherever its data is used) lives here.
 */
export function AboutDialog({ open, onOpenChange, version }: { open: boolean; onOpenChange: (open: boolean) => void; version: string | undefined }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{ui.help.about}</DialogTitle>
          <DialogDescription>{ui.help.aboutDescription}</DialogDescription>
        </DialogHeader>
        <dl className="flex flex-col gap-2 text-sm">
          <div className="flex gap-2">
            <dt className="text-muted-foreground">{ui.help.version}</dt>
            <dd data-testid="app-version">{versionLabel(version)}</dd>
          </div>
        </dl>
        <ul className="flex flex-col gap-1 text-sm text-muted-foreground">
          <li><GeolocationCredit /></li>
          <li><PoweredBySupermetry className="underline-offset-4 hover:text-foreground hover:underline" /></li>
        </ul>
      </DialogContent>
    </Dialog>
  )
}
