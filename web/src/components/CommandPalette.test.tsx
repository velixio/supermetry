import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { CommandPalette } from './CommandPalette'
import type { PaletteCommand } from './paletteCommands'

const commands: PaletteCommand[] = [
  { id: 'page:/events', group: 'pages', label: 'Events', keywords: ['Events'], kind: 'navigate', href: '/app/acme/docs/events' },
  { id: 'page:/funnels', group: 'pages', label: 'Funnels', keywords: ['Funnels'], kind: 'navigate', href: '/app/acme/docs/funnels' },
  { id: 'site:8', group: 'sites', label: 'Shop', detail: 'Acme', keywords: ['Shop', 'Acme'], kind: 'navigate', href: '/app/acme/shop' },
  { id: 'action:theme', group: 'actions', label: 'Use dark theme', keywords: ['Use dark theme'], kind: 'action', action: 'theme' },
]

function Harness({ onRun }: { onRun: (command: PaletteCommand) => void }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>Open palette</button>
      <CommandPalette
        open={open}
        onOpenChange={setOpen}
        commands={commands}
        onRun={(command) => {
          setOpen(false)
          onRun(command)
        }}
      />
    </>
  )
}

beforeEach(() => {
  Element.prototype.scrollIntoView = () => undefined
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

async function open(onRun = vi.fn()) {
  render(<Harness onRun={onRun} />)
  const trigger = screen.getByRole('button', { name: 'Open palette' })
  trigger.focus()
  fireEvent.click(trigger)
  const dialog = await screen.findByRole('dialog', { name: 'Command palette' })
  const input = within(dialog).getByRole('combobox', { name: 'Search commands' })
  return { trigger, dialog, input, onRun }
}

describe('CommandPalette', () => {
  it('is a titled dialog with the search field focused and only non-empty groups', async () => {
    const { dialog, input } = await open()
    await waitFor(() => expect(input).toHaveFocus())
    expect(within(dialog).getByRole('group', { name: 'Pages' })).toBeInTheDocument()
    expect(within(dialog).getByRole('group', { name: 'Sites' })).toBeInTheDocument()
    expect(within(dialog).getByRole('group', { name: 'Actions' })).toBeInTheDocument()
    expect(within(dialog).queryByRole('group', { name: 'Organizations' })).toBeNull()
    expect(within(dialog).getByRole('option', { name: /Shop/ })).toHaveTextContent('Acme')
  })

  it('filters by label as the user types and shows an empty state', async () => {
    const { dialog, input } = await open()
    fireEvent.change(input, { target: { value: 'fun' } })
    await waitFor(() => expect(within(dialog).getAllByRole('option').map((option) => option.textContent)).toEqual(['Funnels']))
    fireEvent.change(input, { target: { value: 'zzzz' } })
    expect(await within(dialog).findByText('No matching commands')).toBeVisible()
  })

  it('moves the selection with the arrow keys and runs the selected command on Enter', async () => {
    const { dialog, input, onRun } = await open()
    await waitFor(() => expect(within(dialog).getByRole('option', { name: 'Events' })).toHaveAttribute('aria-selected', 'true'))
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    expect(within(dialog).getByRole('option', { name: 'Funnels' })).toHaveAttribute('aria-selected', 'true')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onRun).toHaveBeenCalledWith(commands[1])
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument())
  })

  it('closes on Escape and returns focus to the invoker', async () => {
    const { trigger, input, onRun } = await open()
    fireEvent.keyDown(input, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument())
    await waitFor(() => expect(trigger).toHaveFocus())
    expect(onRun).not.toHaveBeenCalled()
  })
})
