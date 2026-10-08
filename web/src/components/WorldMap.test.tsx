import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { WorldMap } from './WorldMap'

describe('WorldMap', () => {
  afterEach(cleanup)

  it('assigns five linear choropleth steps from hand-computed visitor counts', () => {
    const { container } = render(
      <WorldMap
        rows={[
          { country: 'IN', visitors: 1 },
          { country: 'US', visitors: 2 },
          { country: 'BR', visitors: 3 },
          { country: 'DE', visitors: 4 },
          { country: 'AU', visitors: 5 },
        ]}
        onSelect={vi.fn()}
      />,
    )

    expect(container.querySelector('[data-country="IN"]')).toHaveAttribute('data-step', '1')
    expect(container.querySelector('[data-country="US"]')).toHaveAttribute('data-step', '2')
    expect(container.querySelector('[data-country="BR"]')).toHaveAttribute('data-step', '3')
    expect(container.querySelector('[data-country="DE"]')).toHaveAttribute('data-step', '4')
    expect(container.querySelector('[data-country="AU"]')).toHaveAttribute('data-step', '5')
  })

  it('renders an all-neutral map and an empty accessible table without data', () => {
    const { container } = render(<WorldMap rows={[]} onSelect={vi.fn()} />)

    expect(screen.getByRole('group', { name: 'World map' })).toBeVisible()
    expect(screen.queryAllByRole('button')).toHaveLength(0)
    expect(container.querySelectorAll('path[data-step="0"]').length).toBeGreaterThan(100)
    expect(screen.getByRole('table', { name: 'Country visitors' })).toBeInTheDocument()
  })

  it('shows a formatted flag, country name, and visitor count on hover and focus', () => {
    render(<WorldMap rows={[{ country: 'IN', visitors: 12_345 }]} onSelect={vi.fn()} />)
    const country = screen.getByRole('button', { name: 'India: 12.3K visitors' })

    fireEvent.mouseEnter(country)
    expect(screen.getByRole('tooltip')).toHaveTextContent('India12.3K')
    expect(screen.getByRole('tooltip').querySelector('img')).toHaveAttribute('src', '/flags/in.svg')

    fireEvent.mouseLeave(country)
    fireEvent.focus(country)
    expect(screen.getByRole('tooltip')).toHaveTextContent('India12.3K')
  })

  it.each(['Enter', ' '])('selects the focused country with %s', (key) => {
    const onSelect = vi.fn()
    render(<WorldMap rows={[{ country: 'IN', visitors: 12 }]} onSelect={onSelect} />)

    fireEvent.keyDown(screen.getByRole('button', { name: 'India: 12 visitors' }), { key })

    expect(onSelect).toHaveBeenCalledWith('IN')
  })

  it('selects the clicked country code', () => {
    const onSelect = vi.fn()
    render(<WorldMap rows={[{ country: 'US', visitors: 9 }]} onSelect={onSelect} />)

    fireEvent.click(screen.getByRole('button', { name: 'United States: 9 visitors' }))

    expect(onSelect).toHaveBeenCalledWith('US')
  })
})
