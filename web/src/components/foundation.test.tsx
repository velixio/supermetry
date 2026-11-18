import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ConfirmDangerDialog } from './ConfirmDangerDialog'
import { SegmentedControl } from './SegmentedControl'
import { MAIN_CONTENT_ID, SkipLink } from './SkipLink'
import { StatusChip } from './StatusChip'

afterEach(cleanup)

function Segments() {
  const [value, setValue] = useState('chart')
  return (
    <SegmentedControl
      aria-label="Chart view"
      value={value}
      onValueChange={setValue}
      options={[{ value: 'chart', label: 'Chart' }, { value: 'table', label: 'Table' }, { value: 'map', label: 'Map', disabled: true }]}
    />
  )
}

describe('SegmentedControl', () => {
  it('uses tab semantics and marks the selected segment', () => {
    render(<Segments />)
    expect(screen.getByRole('tablist', { name: 'Chart view' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Chart' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'Table' })).toHaveAttribute('aria-selected', 'false')
  })

  it('moves and selects with arrow keys, skipping disabled options, and keeps one tab stop', async () => {
    render(<Segments />)
    screen.getByRole('tab', { name: 'Chart' }).focus()
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'ArrowRight' })
    expect(screen.getByRole('tab', { name: 'Table' })).toHaveFocus()
    expect(screen.getByRole('tab', { name: 'Table' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'Chart' })).toHaveAttribute('tabindex', '-1')
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'ArrowRight' })
    expect(screen.getByRole('tab', { name: 'Chart' })).toHaveFocus()
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'End' })
    expect(screen.getByRole('tab', { name: 'Table' })).toHaveFocus()
  })
})

describe('StatusChip', () => {
  it('renders the text label with an optional decorative icon', () => {
    render(<StatusChip variant="danger" icon={<svg data-testid="i" />}>Failed</StatusChip>)
    const chip = screen.getByText('Failed')
    expect(chip).toHaveAttribute('data-variant', 'danger')
    expect(screen.getByTestId('i').parentElement).toHaveAttribute('aria-hidden', 'true')
  })
})

describe('SkipLink', () => {
  it('links to the main content landmark', () => {
    render(<SkipLink />)
    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveAttribute('href', `#${MAIN_CONTENT_ID}`)
  })
})

describe('ConfirmDangerDialog', () => {
  it('requires the typed name, confirms, and returns focus to the trigger', async () => {
    const onConfirm = vi.fn()
    render(
      <ConfirmDangerDialog
        trigger={<button>Delete site</button>}
        title="Delete example.com?"
        description="This cannot be undone."
        confirmLabel="Delete site"
        confirmText="example.com"
        onConfirm={onConfirm}
      />,
    )
    const trigger = screen.getByRole('button', { name: 'Delete site' })
    fireEvent.click(trigger)
    const confirm = screen.getAllByRole('button', { name: 'Delete site' }).find((b) => b !== trigger)!
    expect(confirm).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Type example.com to confirm'), { target: { value: 'example.com' } })
    expect(confirm).toBeEnabled()
    fireEvent.click(confirm)
    expect(onConfirm).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(trigger).toHaveFocus()
  })

  it('cancel closes without confirming', async () => {
    const onConfirm = vi.fn()
    render(<ConfirmDangerDialog trigger={<button>Remove</button>} title="Remove?" description="d" confirmLabel="Remove member" onConfirm={onConfirm} />)
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onConfirm).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
  })
})
