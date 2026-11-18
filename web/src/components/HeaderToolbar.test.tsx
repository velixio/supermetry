import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { HeaderToolbar, HeaderToolbarContext } from './HeaderToolbar'
import { PageHeader } from './PageHeader'

const width = window.innerWidth
afterEach(() => {
  cleanup()
  document.body.innerHTML = ''
  window.innerWidth = width
})

function renderInShell(ui: React.ReactNode, pageLabel?: string) {
  const slot = document.createElement('div')
  document.body.append(slot)
  render(<HeaderToolbarContext.Provider value={{ node: slot, pageLabel }}>{ui}</HeaderToolbarContext.Provider>)
  return slot
}

describe('HeaderToolbar', () => {
  it('portals controls into the header slot on wide screens', () => {
    window.innerWidth = 1440
    const slot = renderInShell(<HeaderToolbar fallback={<span>inline</span>}><button type="button">Date</button></HeaderToolbar>)
    expect(slot).toContainElement(screen.getByRole('button', { name: 'Date' }))
    expect(screen.queryByText('inline')).toBeNull()
  })

  it('keeps the inline fallback on narrow screens', () => {
    window.innerWidth = 800
    renderInShell(<HeaderToolbar fallback={<span>inline</span>}><button type="button">Date</button></HeaderToolbar>)
    expect(screen.getByText('inline')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Date' })).toBeNull()
  })

  it('folds a page title that repeats the breadcrumb into the header', () => {
    window.innerWidth = 1440
    const slot = renderInShell(<PageHeader title="Events" description="Recent events." actions={<button type="button">Filter</button>} />, 'Events')
    expect(screen.getByRole('heading', { level: 1, name: 'Events' })).toHaveClass('sr-only')
    expect(slot).toContainElement(screen.getByRole('button', { name: 'Filter' }))
  })

  it('keeps a title that differs from the breadcrumb visible', () => {
    window.innerWidth = 1440
    renderInShell(<PageHeader title="Checkout funnel" />, 'Funnels')
    expect(screen.getByRole('heading', { level: 1, name: 'Checkout funnel' })).not.toHaveClass('sr-only')
  })

  it('keeps a folded title\'s actions in one inline row on narrow screens', () => {
    window.innerWidth = 390
    const slot = renderInShell(<PageHeader title="Events" actions={<button type="button">Filter</button>} />, 'Events')
    expect(screen.getByRole('heading', { level: 1, name: 'Events' })).toHaveClass('sr-only')
    expect(slot).toBeEmptyDOMElement()
    expect(screen.getByRole('button', { name: 'Filter' }).parentElement).toHaveClass('flex-nowrap')
  })
})
