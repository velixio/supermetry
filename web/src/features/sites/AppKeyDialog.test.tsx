import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn(async () => ({ events: 0, series: [] })) }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { AppKeyDialog } from './AppKeyDialog'

afterEach(cleanup)

const wrap = (node: React.ReactNode) => render(<QueryClientProvider client={new QueryClient()}>{node}</QueryClientProvider>)

describe('AppKeyDialog setup check', () => {
  it('shows the three-step checklist when the app is known', async () => {
    wrap(<AppKeyDialog appId="pub7" ingestKey="smk_x" siteId={7} timezone="UTC" onClose={() => undefined} />)
    expect(screen.getByText(/Add the key to your build/)).toBeInTheDocument()
    expect(screen.getByText(/Send a test report/)).toBeInTheDocument()
    expect(await screen.findByText('Waiting for first report…')).toBeInTheDocument()
    expect(privateRequest).toHaveBeenCalledWith('GET', expect.stringContaining('/api/private/sites/7/app/overview?'))
  })
  it('omits the checklist without a site id', () => {
    wrap(<AppKeyDialog appId="pub7" ingestKey="smk_x" onClose={() => undefined} />)
    expect(screen.queryByText(/Send a test report/)).not.toBeInTheDocument()
  })
  it('is wide, wraps long code lines and keeps the action buttons outside the scrolling body', () => {
    wrap(<AppKeyDialog appId="pub7" ingestKey="smk_x" siteId={7} timezone="UTC" onClose={() => undefined} />)
    const dialog = screen.getByRole('dialog')
    expect(dialog.className).toContain('sm:max-w-2xl')
    for (const name of ['Copy key', 'Copy snippet', 'Close']) expect(screen.getByRole('button', { name })).toBeInTheDocument()
    const pres = Array.from(dialog.querySelectorAll('pre'))
    expect(pres.length).toBeGreaterThan(1)
    for (const pre of pres) expect(pre.className).toMatch(/break-all|whitespace-pre-wrap/)
    const body = dialog.querySelector('[data-slot="app-key-body"]')
    expect(body?.className).toContain('overflow-y-auto')
    expect(body?.contains(screen.getByRole('button', { name: 'Copy key' }))).toBe(false)
  })
})
