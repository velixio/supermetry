import { PanelRightOpenIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'

/** Opens the dashboard inspector for one value; separate from the row's filter click. */
export function InspectButton({ label, pressed, onInspect }: { label: string; pressed: boolean; onInspect(origin: HTMLElement): void }) {
  return (
    <Button
      aria-label={label}
      aria-pressed={pressed}
      className="shrink-0 text-muted-foreground aria-pressed:bg-muted aria-pressed:text-foreground"
      size="icon-sm"
      title={label}
      type="button"
      variant="ghost"
      onClick={(event) => onInspect(event.currentTarget)}
    >
      <PanelRightOpenIcon aria-hidden="true" />
    </Button>
  )
}
