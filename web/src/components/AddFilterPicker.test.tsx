import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { useSuggestions } = vi.hoisted(() => ({
  useSuggestions: vi.fn(() => ({ data: [] })),
}))

vi.mock('@/features/events/api', () => ({
  useSuggestions,
}))

import { CHANNELS } from '@/lib/channels'

import { AddFilterPicker } from './AddFilterPicker'

describe('AddFilterPicker', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('offers the fixed channel values and adds the selected channel', async () => {
    const onAdd = vi.fn()
    render(
      <AddFilterPicker
        siteId={7}
        orgId={3}
        dateRange={{ from: '2026-10-01', to: '2026-10-08' }}
        onAdd={onAdd}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Add filter' }))
    fireEvent.change(screen.getByLabelText('Dimension'), {
      target: { value: 'channel' },
    })

    const suggestions = await screen.findByRole('group', {
      name: 'Suggested values',
    })
    expect(
      within(suggestions)
        .getAllByRole('button')
        .map((button) => button.textContent),
    ).toEqual(CHANNELS)

    fireEvent.click(
      within(suggestions).getByRole('button', { name: 'Paid Social' }),
    )
    fireEvent.click(screen.getByRole('button', { name: 'Apply filter' }))

    expect(onAdd).toHaveBeenCalledWith({
      dimension: 'channel',
      operator: 'is',
      values: ['Paid Social'],
    })
  })

  it('keeps API suggestions disabled while the debounced dimension is channel', async () => {
    render(
      <AddFilterPicker
        siteId={7}
        orgId={3}
        dateRange={{ from: '2026-10-01', to: '2026-10-08' }}
        onAdd={vi.fn()}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Add filter' }))
    fireEvent.change(screen.getByLabelText('Dimension'), {
      target: { value: 'channel' },
    })

    await vi.waitFor(() =>
      expect(useSuggestions).toHaveBeenLastCalledWith(
        7,
        3,
        { from: '2026-10-01', to: '2026-10-08' },
        'channel',
        '',
        false,
      ),
    )

    fireEvent.change(screen.getByLabelText('Dimension'), {
      target: { value: 'source' },
    })

    expect(useSuggestions).toHaveBeenLastCalledWith(
      7,
      3,
      { from: '2026-10-01', to: '2026-10-08' },
      'channel',
      '',
      false,
    )
  })

  it('lists the new dimensions with readable labels', () => {
    render(
      <AddFilterPicker
        siteId={7}
        orgId={3}
        dateRange={{ from: '2026-10-01', to: '2026-10-08' }}
        onAdd={vi.fn()}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Add filter' }))

    const options = within(screen.getByLabelText('Dimension'))
      .getAllByRole('option')
      .map((option) => [option.getAttribute('value'), option.textContent])
    expect(options).toEqual(expect.arrayContaining([
      ['browser_version', 'Browser version'],
      ['os_version', 'OS version'],
      ['screen_size', 'Screen size'],
      ['language', 'Language'],
      ['hostname', 'Hostname'],
    ]))
  })

  it('offers fixed screen-size buckets and adds the selected bucket', async () => {
    const onAdd = vi.fn()
    render(
      <AddFilterPicker
        siteId={7}
        orgId={3}
        dateRange={{ from: '2026-10-01', to: '2026-10-08' }}
        onAdd={onAdd}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Add filter' }))
    fireEvent.change(screen.getByLabelText('Dimension'), {
      target: { value: 'screen_size' },
    })

    const suggestions = await screen.findByRole('group', {
      name: 'Suggested values',
    })
    expect(
      within(suggestions)
        .getAllByRole('button')
        .map((button) => button.textContent),
    ).toEqual(['Mobile', 'Tablet', 'Laptop', 'Desktop', 'Large'])

    fireEvent.click(within(suggestions).getByRole('button', { name: 'Laptop' }))
    fireEvent.click(screen.getByRole('button', { name: 'Apply filter' }))

    expect(onAdd).toHaveBeenCalledWith({
      dimension: 'screen_size',
      operator: 'is',
      values: ['laptop'],
    })
    expect(useSuggestions).toHaveBeenLastCalledWith(
      7,
      3,
      { from: '2026-10-01', to: '2026-10-08' },
      expect.any(String),
      expect.any(String),
      false,
    )
  })
})
