import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { GeolocationCredit } from './GeolocationCredit'

function renderCredit() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <GeolocationCredit />
    </QueryClientProvider>,
  )
}

function stubConfig(body: unknown, ok = true) {
  const fetchMock = vi.fn(async () => ({ ok, json: async () => body }) as Response)
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('GeolocationCredit', () => {
  it('credits DB-IP when the bundled database is in use', async () => {
    stubConfig({ geoip_source: 'dbip' })
    renderCredit()
    expect(await screen.findByRole('link', { name: 'IP geolocation by DB-IP.com' })).toHaveAttribute(
      'href',
      'https://db-ip.com',
    )
    expect(screen.queryByText(/MaxMind/)).not.toBeInTheDocument()
  })

  it("shows MaxMind's required notice when GeoLite data is in use", async () => {
    stubConfig({ geoip_source: 'maxmind' })
    renderCredit()
    expect(
      await screen.findByText(/This product includes GeoLite Data created by MaxMind, available from/),
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'https://www.maxmind.com' })).toHaveAttribute(
      'href',
      'https://www.maxmind.com',
    )
    expect(screen.queryByRole('link', { name: 'IP geolocation by DB-IP.com' })).not.toBeInTheDocument()
  })

  it('falls back to the DB-IP credit when the config cannot be read', async () => {
    stubConfig({}, false)
    renderCredit()
    expect(await screen.findByRole('link', { name: 'IP geolocation by DB-IP.com' })).toBeInTheDocument()
  })

  it('falls back to the DB-IP credit for an unknown source', async () => {
    stubConfig({ geoip_source: '<script>' })
    renderCredit()
    expect(await screen.findByRole('link', { name: 'IP geolocation by DB-IP.com' })).toBeInTheDocument()
  })

  it('shows nothing until the source is known, so the wrong provider is never credited', async () => {
    let release: (value: Response) => void = () => undefined
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { release = resolve })))
    const { container } = renderCredit()
    expect(container).toBeEmptyDOMElement()
    release({ ok: true, json: async () => ({ geoip_source: 'maxmind' }) } as Response)
    await waitFor(() => expect(screen.getByText(/created by MaxMind/)).toBeInTheDocument())
  })
})
