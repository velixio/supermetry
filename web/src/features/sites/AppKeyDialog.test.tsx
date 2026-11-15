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
})
