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

  it('crops Antarctica and expands the inhabited map into the viewport', () => {
    const { container } = render(<WorldMap rows={[]} onSelect={vi.fn()} />)
    const map = screen.getByRole('group', { name: 'World map' })

    expect(container.querySelector('[data-country="AQ"]')).not.toBeInTheDocument()
    expect(map).toHaveAttribute('viewBox', '0 0 1000 445')
  })

  it('shows a formatted flag, country name, and visitor count in a portalled pointer-safe tooltip', () => {
    render(<WorldMap rows={[{ country: 'IN', visitors: 12_345 }]} onSelect={vi.fn()} />)
    const country = screen.getByRole('button', { name: 'India: 12.3K visitors' })

    fireEvent.mouseEnter(country)
    expect(screen.getByRole('tooltip')).toHaveTextContent('India12.3K')
    expect(screen.getByRole('tooltip').querySelector('img')).toHaveAttribute('src', '/flags/in.svg')
    expect(screen.getByRole('tooltip').parentElement).toBe(document.body)
    expect(screen.getByRole('tooltip')).toHaveClass('pointer-events-none')

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

  it('shows metric legend values and announces the share of total', () => {
    render(<WorldMap metric="pageviews" rows={[{ country: 'IN', visitors: 10, pageviews: 1_234 }, { country: 'US', visitors: 20, pageviews: 8_766 }]} showLegend onSelect={vi.fn()} />)
    expect(screen.getByLabelText('Map legend')).toHaveTextContent('0Pageviews8,766')
    expect(screen.getByRole('button', { name: 'India: 1,234 pageviews, 12.3 percent' })).toBeInTheDocument()
  })

  it('clears selection with Escape and visibly marks the selected country', () => {
    const onClear = vi.fn()
    const { container } = render(<WorldMap rows={[{ country: 'IN', visitors: 12 }]} selectedCountry="IN" showLegend onClear={onClear} onSelect={vi.fn()} />)
    const country = screen.getByRole('button', { name: /India: 12 visitors, 100 percent/ })
    expect(container.querySelector('[data-country="IN"]')).toHaveAttribute('aria-pressed', 'true')
    fireEvent.keyDown(country, { key: 'Escape' })
    expect(onClear).toHaveBeenCalledOnce()
  })

  it('draws the selected country last with a thin non-scaling interaction ring', () => {
    const { container } = render(
      <WorldMap
        rows={[
          { country: 'US', visitors: 100 },
          { country: 'IN', visitors: 20 },
        ]}
        selectedCountry="US"
        onSelect={vi.fn()}
      />,
    )
    const selected = container.querySelector('[data-country="US"]')
    const svg = screen.getByRole('group', { name: 'World map' })

    expect(svg.lastElementChild).toBe(selected)
    expect(selected).toHaveAttribute('vector-effect', 'non-scaling-stroke')
    expect(selected).toHaveClass('stroke-[2]')
    expect(selected).toHaveClass('hover:stroke-[2]')
    expect(selected).toHaveClass('focus-visible:stroke-[2]')
    expect(selected).toHaveClass('stroke-ring')
    expect(selected).toHaveClass('hover:stroke-ring')
    expect(selected).not.toHaveClass('stroke-[3]')
    expect(selected).not.toHaveClass('stroke-foreground')
  })

  it('links map hover and focus to an externally highlighted country', () => {
    const onActiveChange = vi.fn()
    const { container, rerender } = render(
      <WorldMap rows={[{ country: 'IN', visitors: 12 }]} onActiveChange={onActiveChange} onSelect={vi.fn()} />,
    )
    const india = screen.getByRole('button', { name: 'India: 12 visitors' })
    fireEvent.mouseEnter(india)
    expect(onActiveChange).toHaveBeenLastCalledWith('IN')
    fireEvent.mouseLeave(india)
    expect(onActiveChange).toHaveBeenLastCalledWith(undefined)

    rerender(<WorldMap activeCountry="IN" rows={[{ country: 'IN', visitors: 12 }]} onActiveChange={onActiveChange} onSelect={vi.fn()} />)
    expect(container.querySelector('[data-country="IN"]')).toHaveAttribute('data-active', 'true')
  })
})
