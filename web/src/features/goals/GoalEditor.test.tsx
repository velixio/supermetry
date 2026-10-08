import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { GoalEditor } from './GoalEditor'

describe('GoalEditor', () => {
  afterEach(() => cleanup())

  it('saves an event revenue goal with a common currency', async () => {
    const onSave = vi.fn()
    render(<GoalEditor pending={false} onSave={onSave} />)

    fireEvent.change(screen.getByLabelText('Goal name'), { target: { value: 'Purchases' } })
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'event' } })
    fireEvent.change(screen.getByLabelText('Goal value'), { target: { value: 'purchase' } })
    fireEvent.change(screen.getByLabelText('Revenue currency'), { target: { value: 'USD' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create goal' }))

    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ currency: 'USD' })))
  })

  it('accepts a custom three-letter currency and normalizes it to uppercase', async () => {
    const onSave = vi.fn()
    render(<GoalEditor pending={false} onSave={onSave} />)

    fireEvent.change(screen.getByLabelText('Goal name'), { target: { value: 'Purchases' } })
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'event' } })
    fireEvent.change(screen.getByLabelText('Goal value'), { target: { value: 'purchase' } })
    fireEvent.change(screen.getByLabelText('Revenue currency'), { target: { value: 'custom' } })
    fireEvent.change(screen.getByLabelText('Custom currency'), { target: { value: 'inr' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create goal' }))

    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ currency: 'INR' })))
  })

  it('clears revenue currency when an event goal changes to a page goal', async () => {
    const onSave = vi.fn()
    render(<GoalEditor pending={false} onSave={onSave} />)

    fireEvent.change(screen.getByLabelText('Goal name'), { target: { value: 'Thank you' } })
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'event' } })
    fireEvent.change(screen.getByLabelText('Revenue currency'), { target: { value: 'USD' } })
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'page' } })
    fireEvent.change(screen.getByLabelText('Goal value'), { target: { value: '/thanks' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create goal' }))

    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.not.objectContaining({ currency: expect.anything() })))
    expect(screen.queryByLabelText('Revenue currency')).not.toBeInTheDocument()
  })
})
