import { useState, type ReactNode } from 'react'

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { interpolate } from '@/lib/i18n'
import { uiFoundation } from '@/messages/uiFoundation'

type ConfirmDangerDialogProps = {
  /** The element that opens the dialog (rendered with asChild); focus returns to it on close. */
  trigger: ReactNode
  title: string
  description: ReactNode
  /** Label of the danger button: a verb, e.g. "Delete site". */
  confirmLabel: string
  /** Run on confirm; the dialog stays open and the button shows busy while it is pending. */
  onConfirm: () => void | Promise<void>
  /** When set, the danger button stays disabled until the user types exactly this text. */
  confirmText?: string
  /** Controlled open state (optional). */
  open?: boolean
  onOpenChange?: (open: boolean) => void
}

export function ConfirmDangerDialog({
  trigger,
  title,
  description,
  confirmLabel,
  onConfirm,
  confirmText,
  open: openProp,
  onOpenChange,
}: ConfirmDangerDialogProps) {
  const [innerOpen, setInnerOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const open = openProp ?? innerOpen

  function setOpen(next: boolean) {
    if (busy) return
    if (!next) setTyped('')
    setInnerOpen(next)
    onOpenChange?.(next)
  }

  async function confirm() {
    setBusy(true)
    try {
      await onConfirm()
      setBusy(false)
      setTyped('')
      setInnerOpen(false)
      onOpenChange?.(false)
    } catch {
      setBusy(false)
    }
  }

  const ready = confirmText === undefined || typed === confirmText

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {confirmText !== undefined ? (
          <div className="grid gap-2">
            <Label htmlFor="confirm-danger-input">
              {interpolate(uiFoundation.confirm.typeToConfirm, { name: confirmText })}
            </Label>
            <Input
              id="confirm-danger-input"
              value={typed}
              onChange={(event) => setTyped(event.target.value)}
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
            />
          </div>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>{uiFoundation.confirm.cancel}</AlertDialogCancel>
          <Button variant="destructive" disabled={!ready || busy} aria-busy={busy} onClick={() => void confirm()}>
            {confirmLabel}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
