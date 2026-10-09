import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { GoalEditor } from './GoalEditor'

describe('GoalEditor', () => {
  afterEach(() => cleanup())

  it('offers page, event, and scroll-depth goal types', () => {
    render(<GoalEditor pending={false} onSave={vi.fn()} />)

    const options = screen.getAllByRole('option').map((option) => option.textContent)
    expect(options).toEqual(expect.arrayContaining(['Page', 'Event', 'Scroll depth']))
  })

  it('saves a scroll-depth goal with a required bounded threshold and no currency', async () => {
    const onSave = vi.fn()
    render(<GoalEditor pending={false} onSave={onSave} />)

    fireEvent.change(screen.getByLabelText('Goal name'), { target: { value: 'Read blog' } })
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'scroll' } })
    fireEvent.change(screen.getByLabelText('Goal value'), { target: { value: '/blog/*' } })

    const threshold = screen.getByLabelText('Scroll threshold (%)')
    expect(threshold).toHaveAttribute('type', 'number')
    expect(threshold).toHaveAttribute('min', '1')
    expect(threshold).toHaveAttribute('max', '100')
    expect(threshold).toBeRequired()
    expect(screen.getByText('Counts a visit when someone scrolls at least this far on a matching page')).toBeVisible()
    expect(screen.queryByLabelText('Track revenue')).not.toBeInTheDocument()

    fireEvent.change(threshold, { target: { value: '75' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create goal' }))

    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'scroll',
      scroll_threshold: 75,
    })))
    expect(onSave).toHaveBeenCalledWith(expect.not.objectContaining({ currency: expect.anything() }))
  })

  it('clears the scroll threshold before saving after switching back to page', async () => {
    const onSave = vi.fn()
    render(<GoalEditor pending={false} onSave={onSave} projectCurrency="USD" currencyState="configured" />)

    fireEvent.change(screen.getByLabelText('Goal name'), { target: { value: 'Read blog' } })
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'scroll' } })
    fireEvent.change(screen.getByLabelText('Goal value'), { target: { value: '/blog/*' } })
    fireEvent.change(screen.getByLabelText('Scroll threshold (%)'), { target: { value: '75' } })
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'page' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create goal' }))

    expect(screen.queryByLabelText('Scroll threshold (%)')).not.toBeInTheDocument()
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.not.objectContaining({
      scroll_threshold: expect.anything(),
    })))
  })

  it('saves an event revenue goal with a common currency', async () => {
    const onSave = vi.fn()
    render(<GoalEditor pending={false} onSave={onSave} projectCurrency="USD" currencyState="configured" />)

    fireEvent.change(screen.getByLabelText('Goal name'), { target: { value: 'Purchases' } })
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'event' } })
    fireEvent.change(screen.getByLabelText('Goal value'), { target: { value: 'purchase' } })
    fireEvent.click(screen.getByLabelText('Track revenue'))
    fireEvent.click(screen.getByRole('button', { name: 'Create goal' }))

    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ currency: 'USD' })))
  })

  it('uses the project currency without offering a per-goal currency picker', () => {
    render(<GoalEditor pending={false} onSave={vi.fn()} projectCurrency="INR" currencyState="configured" />)
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'event' } })
    expect(screen.getByText(/permanent INR currency/)).toBeVisible()
    expect(screen.queryByLabelText('Custom currency')).not.toBeInTheDocument()
  })

  it('clears revenue currency when an event goal changes to a page goal', async () => {
    const onSave = vi.fn()
    render(<GoalEditor pending={false} onSave={onSave} projectCurrency="USD" currencyState="configured" />)

    fireEvent.change(screen.getByLabelText('Goal name'), { target: { value: 'Thank you' } })
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'event' } })
    fireEvent.click(screen.getByLabelText('Track revenue'))
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'page' } })
    fireEvent.change(screen.getByLabelText('Goal value'), { target: { value: '/thanks' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create goal' }))

    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.not.objectContaining({ currency: expect.anything() })))
    expect(screen.queryByLabelText('Track revenue')).not.toBeInTheDocument()
  })

  it('allows a conflicted legacy revenue goal to disable revenue', async () => {
    const onSave = vi.fn()
    render(<GoalEditor
      goal={{ id: 1, site_id: 1, kind: 'event', match: 'exact', value: 'purchase', name: 'Purchase', currency: 'EUR' }}
      pending={false}
      onSave={onSave}
      currencyState="conflict"
    />)

    const revenue = screen.getByLabelText('Track revenue')
    expect(revenue).toBeEnabled()
    fireEvent.click(revenue)
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.not.objectContaining({ currency: expect.anything() })))
  })
})
