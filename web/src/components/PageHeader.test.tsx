import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { PageContainer } from './PageContainer'
import { PageHeader } from './PageHeader'

afterEach(cleanup)

describe('PageHeader', () => {
  it('renders one level-one heading with the standard title style', () => {
    render(<PageHeader title="Funnels" />)
    const heading = screen.getByRole('heading', { level: 1, name: 'Funnels' })
    expect(heading).toHaveClass('text-xl', 'font-semibold', 'tracking-tight')
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1)
  })

  it('renders the description and actions when given', () => {
    render(<PageHeader title="Goals" description="Last 30 days" actions={<button type="button">New goal</button>} />)
    expect(screen.getByText('Last 30 days')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New goal' })).toBeInTheDocument()
  })

  it('renders no description or actions wrapper when they are absent', () => {
    const { container } = render(<PageHeader title="Goals" />)
    expect(container.querySelector('p')).toBeNull()
    expect(container.querySelector('[data-slot="page-header-actions"]')).toBeNull()
  })

  it('lets a very long title wrap without hiding the actions', () => {
    const title = 'A'.repeat(120)
    render(<PageHeader title={title} actions={<button type="button">Act</button>} />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveClass('break-words')
    expect(screen.getByRole('button', { name: 'Act' })).toBeInTheDocument()
  })
})

describe('PageContainer', () => {
  it('is wide by default and narrow on request', () => {
    const { rerender, container } = render(<PageContainer>x</PageContainer>)
    expect(container.firstElementChild).toHaveClass('max-w-screen-2xl', 'gap-4', 'min-w-0')
    rerender(<PageContainer width="narrow">x</PageContainer>)
    expect(container.firstElementChild).toHaveClass('max-w-5xl')
  })

  it('merges a caller class name', () => {
    const { container } = render(<PageContainer className="extra">x</PageContainer>)
    expect(container.firstElementChild).toHaveClass('extra')
  })
})
