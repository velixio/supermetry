import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { PageContainer } from './PageContainer'
import { PageHeader } from './PageHeader'

afterEach(cleanup)

describe('PageHeader', () => {
  it('renders one level-one heading with the standard title style', () => {
    render(<PageHeader title="Funnels" />)
    const heading = screen.getByRole('heading', { level: 1, name: 'Funnels' })
    expect(heading).toHaveClass('text-base', 'font-semibold', 'tracking-tight')
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1)
  })

  it('keeps the title and its description InfoTip in one compact row, with the actions wrapping on narrow screens', () => {
    const { container } = render(<PageHeader title="Goals" description="Last 30 days" actions={<button type="button">New goal</button>} />)
    const header = container.querySelector('header')
    expect(header).toHaveClass('flex-wrap', 'items-center', 'gap-y-2')
    const heading = screen.getByRole('heading', { level: 1, name: 'Goals' })
    // The description is an InfoTip beside the title, not a second line of text.
    expect(heading.parentElement).toHaveClass('min-w-0', 'items-center')
    expect(heading.parentElement).not.toHaveClass('flex-wrap')
    expect(screen.getByRole('button', { name: 'About Goals' }).parentElement).toBe(heading.parentElement)
    expect(screen.queryByText('Last 30 days')).not.toBeInTheDocument()
  })

  it('renders the description in an InfoTip and the actions when given', async () => {
    render(<PageHeader title="Goals" description="Last 30 days" actions={<button type="button">New goal</button>} />)
    fireEvent.click(screen.getByRole('button', { name: 'About Goals' }))
    expect((await screen.findAllByText('Last 30 days')).length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: 'New goal' })).toBeInTheDocument()
  })

  it('hides an actions wrapper whose children all render nothing', () => {
    const { container } = render(<PageHeader title="Goals" actions={<>{null}</>} />)
    const wrapper = container.querySelector('[data-slot="page-header-actions"]')
    expect(wrapper).toHaveClass('empty:hidden')
    expect(wrapper?.childNodes).toHaveLength(0)
  })

  it('sets the heading id from titleId', () => {
    render(<PageHeader title="Goals" titleId="goals-title" />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveAttribute('id', 'goals-title')
  })

  it('renders no description or actions wrapper when they are absent', () => {
    const { container } = render(<PageHeader title="Goals" />)
    expect(container.querySelector('p')).toBeNull()
    expect(container.querySelector('[data-slot="page-header-actions"]')).toBeNull()
  })

  it('lets a very long title wrap without hiding the actions', () => {
    const title = 'A'.repeat(120)
    render(<PageHeader title={title} actions={<button type="button">Act</button>} />)
    // overflow-wrap:anywhere lets the flex item shrink below a long unbroken word on narrow screens.
    expect(screen.getByRole('heading', { level: 1 })).toHaveClass('wrap-anywhere', 'min-w-0')
    expect(screen.getByRole('button', { name: 'Act' })).toBeInTheDocument()
  })
})

describe('PageContainer', () => {
  it('is wide by default and narrow on request', () => {
    const { rerender, container } = render(<PageContainer>x</PageContainer>)
    expect(container.firstElementChild).toHaveClass('max-w-screen-2xl', 'gap-3', 'min-w-0')
    rerender(<PageContainer width="narrow">x</PageContainer>)
    expect(container.firstElementChild).toHaveClass('max-w-5xl')
  })

  it('merges a caller class name', () => {
    const { container } = render(<PageContainer className="extra">x</PageContainer>)
    expect(container.firstElementChild).toHaveClass('extra')
  })
})
