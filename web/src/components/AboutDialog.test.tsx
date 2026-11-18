import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { AboutDialog } from './AboutDialog'

function show(source: 'maxmind' | 'dbip') {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({ geoip_source: source }) }) as Response))
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AboutDialog open onOpenChange={() => undefined} version="1.4.2" />
    </QueryClientProvider>,
  )
}

afterEach(() => { cleanup(); vi.unstubAllGlobals() })

describe('AboutDialog', () => {
  it('keeps the MaxMind attribution the licence requires, with the full notice as its title', async () => {
    show('maxmind')
    const link = await screen.findByRole('link', { name: 'IP geolocation by MaxMind' })
    expect(link).toHaveAttribute('href', 'https://www.maxmind.com')
    expect(link).toHaveAttribute('title', expect.stringContaining('GeoLite Data created by MaxMind'))
  })

  it('shows the version and the powered-by link', async () => {
    show('dbip')
    expect(await screen.findByRole('dialog', { name: 'About Supermetry' })).toBeVisible()
    expect(screen.getByTestId('app-version')).toHaveTextContent('v1.4.2')
    expect(screen.getByRole('link', { name: 'Powered by Supermetry' })).toBeVisible()
    expect(await screen.findByRole('link', { name: 'IP geolocation by DB-IP.com' })).toBeVisible()
  })
})
