import { useLayoutEffect, useRef } from 'react'
import { Building2Icon, CircleHelpIcon, FileTextIcon, Globe2Icon, LogOutIcon, SunMoonIcon } from 'lucide-react'

import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ui } from '@/messages/ui'

import { paletteGroups, type PaletteCommand } from './paletteCommands'

function CommandIcon({ command }: { command: PaletteCommand }) {
  if (command.kind === 'action') {
    if (command.action === 'theme') return <SunMoonIcon aria-hidden="true" />
    if (command.action === 'help') return <CircleHelpIcon aria-hidden="true" />
    return <LogOutIcon aria-hidden="true" />
  }
  if (command.group === 'sites') return <Globe2Icon aria-hidden="true" />
  if (command.group === 'organizations') return <Building2Icon aria-hidden="true" />
  return <FileTextIcon aria-hidden="true" />
}

/**
 * The global command palette: the app's only global search. Presentational: the caller supplies
 * the commands (see paletteCommands) and runs the chosen one. Radix returns focus to the invoker on close.
 */
export function CommandPalette({
  open,
  onOpenChange,
  commands,
  onRun,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  commands: PaletteCommand[]
  onRun: (command: PaletteCommand) => void
}) {
  // The palette opens from a global shortcut or a button, not a DialogTrigger, so Radix has no
  // trigger to refocus on close. Remember the invoker before focus moves into the dialog.
  const invoker = useRef<HTMLElement | null>(null)
  useLayoutEffect(() => {
    if (open) invoker.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
  }, [open])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="top-[20%] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-lg" showCloseButton={false}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          if (invoker.current?.isConnected) invoker.current.focus()
        }}
      >
        <DialogTitle className="sr-only">{ui.palette.title}</DialogTitle>
        <DialogDescription className="sr-only">{ui.palette.description}</DialogDescription>
        <Command label={ui.palette.inputLabel}>
          <CommandInput placeholder={ui.palette.placeholder} />
          <CommandList>
            <CommandEmpty>{ui.palette.empty}</CommandEmpty>
            {paletteGroups.map((group) => {
              const items = commands.filter((command) => command.group === group)
              if (items.length === 0) return null
              return (
                <CommandGroup key={group} heading={ui.palette.groups[group]}>
                  {items.map((command) => (
                    <CommandItem key={command.id} keywords={command.keywords} value={command.id} onSelect={() => onRun(command)}>
                      <CommandIcon command={command} />
                      <span className="min-w-0 flex-1 truncate">{command.label}</span>
                      {command.detail ? <span className="truncate text-xs text-muted-foreground">{command.detail}</span> : null}
                    </CommandItem>
                  ))}
                </CommandGroup>
              )
            })}
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  )
}
