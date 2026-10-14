import { useLayoutEffect, useRef } from 'react'
import { Building2Icon, CircleHelpIcon, FileTextIcon, Globe2Icon, LogOutIcon, SunMoonIcon } from 'lucide-react'

import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ui } from '@/messages/ui'

import { paletteGroups, type PaletteAction, type PaletteCommand } from './paletteCommands'

const actionIcons = {
  theme: SunMoonIcon,
  help: CircleHelpIcon,
  logout: LogOutIcon,
} satisfies Record<PaletteAction, unknown>

function CommandIcon({ command }: { command: PaletteCommand }) {
  if (command.kind === 'action') {
    const Icon = actionIcons[command.action]
    return <Icon aria-hidden="true" />
  }
  if (command.group === 'sites') return <Globe2Icon aria-hidden="true" />
  if (command.group === 'organizations') return <Building2Icon aria-hidden="true" />
  return <FileTextIcon aria-hidden="true" />
}

function fold(text: string): string {
  return text.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase()
}

/**
 * cmdk filter: scores only the visible text (label, detail, keywords, all passed as `keywords`)
 * and ignores `value`, which is the internal command id. Prefix > word start > substring; 0 hides.
 */
function scoreCommand(_value: string, search: string, keywords: string[] = []): number {
  const query = fold(search.trim())
  if (query === '') return 1
  let best = 0
  for (const text of keywords) {
    const folded = fold(text)
    const at = folded.indexOf(query)
    if (at === 0) return 1
    if (at > 0) best = Math.max(best, /[\s\-_/.]/.test(folded[at - 1]) ? 0.8 : 0.6)
  }
  return best
}

/**
 * The global command palette: the app's only global search. Presentational: the caller supplies
 * the commands (see paletteCommands) and runs the chosen one. There is no DialogTrigger, so the
 * component restores focus to the invoker itself on close, unless a command ran.
 */
export function CommandPalette({
  open,
  onOpenChange,
  commands,
  onRun,
  fallbackFocus,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  commands: PaletteCommand[]
  onRun: (command: PaletteCommand) => void
  /** Where focus goes after a command that neither navigates nor opens a dialog (theme) when the invoker is gone or was the page body. */
  fallbackFocus?: () => HTMLElement | null
}) {
  // The palette opens from a global shortcut or a button, not a DialogTrigger, so Radix has no
  // trigger to refocus on close. Remember the invoker before focus moves into the dialog.
  const invoker = useRef<HTMLElement | null>(null)
  const ran = useRef<PaletteCommand | null>(null)
  useLayoutEffect(() => {
    if (open) {
      invoker.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
      ran.current = null
    }
  }, [open])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="top-[20%] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-lg" showCloseButton={false}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          // After a command ran, the caller owns focus (navigation moves it with the route, help opens
          // its own dialog). Only a command that changes nothing focusable (theme) must put it back,
          // or it would be stranded on the body.
          const command = ran.current
          const previous = invoker.current?.isConnected && invoker.current !== document.body ? invoker.current : null
          if (!command) {
            if (invoker.current?.isConnected) invoker.current.focus()
          }
          else if (command.kind === 'action' && command.action === 'theme') (previous ?? fallbackFocus?.())?.focus()
        }}
      >
        <DialogTitle className="sr-only">{ui.palette.title}</DialogTitle>
        <DialogDescription className="sr-only">{ui.palette.description}</DialogDescription>
        <Command label={ui.palette.inputLabel} filter={scoreCommand}>
          <CommandInput placeholder={ui.palette.placeholder} />
          <CommandList>
            <CommandEmpty>{ui.palette.empty}</CommandEmpty>
            {paletteGroups.map((group) => {
              const items = commands.filter((command) => command.group === group)
              if (items.length === 0) return null
              return (
                <CommandGroup key={group} heading={ui.palette.groups[group]}>
                  {items.map((command) => (
                    <CommandItem key={command.id} keywords={[command.label, ...(command.detail ? [command.detail] : []), ...command.keywords]}
                      value={command.id}
                      onSelect={() => {
                        ran.current = command
                        onRun(command)
                      }}>
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
