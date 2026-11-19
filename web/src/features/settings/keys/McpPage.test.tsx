import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'

vi.mock('@/lib/api', () => ({ api: { POST: vi.fn(), GET: vi.fn(), DELETE: vi.fn() } }))

import { McpPage } from './McpPage'

beforeAll(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  HTMLElement.prototype.scrollIntoView = vi.fn()
})
afterEach(cleanup)

function renderPage(role = 'owner') {
  render(<QueryClientProvider client={new QueryClient()}><McpPage orgId={3} role={role} sites={[{ id: 1, name: 'A' }]} /></QueryClientProvider>)
}

describe('MCP page', () => {
  it('shows the placeholder config per client with a copy button and live region', () => {
    renderPage()
    expect(screen.getByText(/Bearer <key>/)).toBeVisible()
    expect(screen.getByText(/\/api\/public\/v1\/mcp/)).toBeVisible()
    fireEvent.click(screen.getByRole('tab', { name: 'Cursor' }))
    expect(screen.getByRole('tabpanel')).toHaveTextContent('mcpServers')
    expect(screen.getByRole('button', { name: 'Copy Cursor configuration' })).toBeVisible()
    expect(screen.getAllByRole('status').length).toBeGreaterThan(0)
  })

  it('announces Copied after copying', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn().mockResolvedValue(undefined) } })
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Copy Claude Desktop configuration' }))
    expect(await screen.findByText('Copied')).toBeInTheDocument()
  })

  it('opens the wizard preset to MCP from "Create a key for this"', async () => {
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Create a key for this' }))
    const dialog = await screen.findByRole('dialog', { name: 'Create API key' })
    expect(dialog).toHaveTextContent('Step 2 of 3: Access')
    expect(screen.getByLabelText('Key name')).toHaveValue('MCP assistant')
  })

  it('hides the create button from non-managers', () => {
    renderPage('viewer')
    expect(screen.queryByRole('button', { name: 'Create a key for this' })).not.toBeInTheDocument()
  })
})
