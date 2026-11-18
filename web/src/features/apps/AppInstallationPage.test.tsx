import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => <a href={to.replace('$org', params.org).replace('$site', params.site)}>{children}</a>,
}))
const dsnKey = { public_key: 'a'.repeat(32), prefix: 'aaaaaaaa', dsn: 'https://' + 'a'.repeat(32) + '@app.example.com/19', label: 'Production', allowed_origins: ['https://app.example.com'], created_at: '2026-11-15T00:00:00Z', expires_at: null }
const request = vi.hoisted(() => vi.fn())
vi.mock('@/features/settings/privateApi', () => ({ privateRequest: request }))
beforeEach(() => {
  request.mockReset()
  request.mockImplementation(async (method: string, path: string) => {
    if (path.endsWith('/app/dsn-keys')) return method === 'GET' ? { keys: [dsnKey] } : dsnKey
    if (method === 'DELETE') return undefined
    return {
      keys: [
        { prefix: 'abcd1234', created_at: '2026-11-15T00:00:00Z', expires_at: null },
        { prefix: 'old00000', created_at: '2026-11-01T00:00:00Z', expires_at: '2026-11-22T00:00:00Z' },
      ],
    }
  })
})
const mounts = vi.hoisted(() => ({ count: 0 }))
vi.mock('./TestReportCheck', async () => {
  const { useEffect } = await import('react')
  return {
    TestReportCheck: ({ appId }: { appId: string }) => {
      useEffect(() => { mounts.count += 1 }, [])
      return <p>check for {appId}</p>
    },
  }
})

import { AppInstallationPage, appInstallSnippets } from './AppInstallationPage'
import { otelEnvSnippet, otlpEndpoint, sdkSnippets, tracingSnippets } from './sdkSnippets'

afterEach(cleanup)

describe('appInstallSnippets', () => {
  const snippets = appInstallSnippets('6quqxuelthfz', 'https://app.supermetry.com')

  it('fills the app ID and host into every kit, never a real key', () => {
    for (const code of [snippets.typescript, snippets.python, snippets.http]) {
      expect(code).toContain('6quqxuelthfz')
      expect(code).toContain('https://app.supermetry.com')
      expect(code).toContain('<your key>')
      expect(code).not.toMatch(/smk_[a-z2-7]{39}/)
    }
  })

  it('keeps Node-only process APIs out of the browser path', () => {
    expect(snippets.typescript).toContain('// Node and the Electron main process:')
    expect(snippets.typescript).toContain('// window.addEventListener("error"')
  })

  it('uses each kit’s own option names', () => {
    expect(snippets.typescript).toContain('appId: "6quqxuelthfz"')
    expect(snippets.typescript).toContain('import { init, startSession')
    expect(snippets.python).toContain('app_id="6quqxuelthfz"')
    expect(snippets.python).toContain('sm.start_session()')
  })

  it('posts the documented wire format over HTTP', () => {
    expect(snippets.http).toContain('POST https://app.supermetry.com/api/public/v1/app-events')
    expect(snippets.http).toContain('X-Supermetry-Key: <your key>')
    expect(snippets.http).toContain('"site": "6quqxuelthfz"')
  })
})

describe('AppInstallationPage', () => {
  it('shows the app ID, host, current key prefix and the rotate link', async () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <AppInstallationPage org="acme" site="desktop" orgId={1} siteId={19} publicId="6quqxuelthfz" timezone="UTC" />
      </QueryClientProvider>,
    )
    // The Sentry setup comes first: DSN with copy, snippets that set only dsn and release.
    expect(await screen.findByLabelText('DSN')).toHaveTextContent(dsnKey.dsn)
    expect(screen.getByRole('button', { name: 'Copy DSN' })).toBeInTheDocument()
    expect(screen.getByText(/Without it, crash-free rates fall back to error events\./)).toBeInTheDocument()
    expect(screen.getByText(/Sentry SDKs count an app open per session when/)).toHaveTextContent('Use track() and screen() from the Supermetry kits for custom events and screens.')
    expect(screen.getByLabelText('JavaScript (browser) setup')).toHaveTextContent(`dsn: "${dsnKey.dsn}"`)
    expect(screen.getByLabelText('JavaScript (browser) setup')).toHaveTextContent('release:')
    expect(screen.getByLabelText('App ID')).toHaveTextContent('6quqxuelthfz')
    expect(screen.getByLabelText('Host')).toHaveTextContent(window.location.origin)
    expect(await screen.findByText('smk_abcd1234…')).toBeInTheDocument()
    // After a rotation the old key is listed with its end date.
    expect(screen.getByText('smk_old00000…')).toBeInTheDocument()
    expect(screen.getByText(`valid until ${new Date('2026-11-22T00:00:00Z').toLocaleDateString()}`)).toBeInTheDocument()
    expect(screen.getAllByText(/^valid until/)).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'Rotate the key in App settings' })).toHaveAttribute('href', '/app/acme/desktop/settings/app')
    expect(screen.getByText('check for 6quqxuelthfz')).toBeInTheDocument()
    // The old kits live in a collapsed section.
    const legacy = screen.getByText('Legacy kits').closest('details')!
    expect(legacy).not.toHaveAttribute('open')
    expect(legacy).toContainElement(screen.getByLabelText('App ID'))
    // The kits are not published yet: the TypeScript tab builds and packs the kit before installing it.
    expect(screen.getByLabelText('TypeScript install')).toHaveTextContent('npm run build && npm pack')
  })

  it('restarts the first-report check when the app changes', () => {
    const client = new QueryClient()
    const page = (siteId: number, publicId: string) => (
      <QueryClientProvider client={client}>
        <AppInstallationPage org="acme" site="desktop" orgId={1} siteId={siteId} publicId={publicId} timezone="UTC" />
      </QueryClientProvider>
    )
    mounts.count = 0
    const { rerender } = render(page(19, 'appone000001'))
    rerender(page(19, 'appone000001'))
    expect(mounts.count).toBe(1)
    rerender(page(20, 'apptwo000002'))
    expect(mounts.count).toBe(2)
  })

  it('creates a key with allowed origins and revokes after confirming', async () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <AppInstallationPage org="acme" site="desktop" orgId={1} siteId={19} publicId="6quqxuelthfz" timezone="UTC" />
      </QueryClientProvider>,
    )
    await screen.findByLabelText('DSN')
    fireEvent.change(screen.getByLabelText('Label (optional)'), { target: { value: 'Staging' } })
    fireEvent.change(screen.getByLabelText('Allowed origins (optional)'), { target: { value: 'https://a.example.com\nhttps://b.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('POST', '/api/private/sites/19/app/dsn-keys', { orgId: 1, body: { label: 'Staging', allowed_origins: ['https://a.example.com', 'https://b.example.com'] } }))
    fireEvent.click(screen.getByRole('button', { name: 'Revoke key aaaaaaaa' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke key' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('DELETE', '/api/private/sites/19/app/dsn-keys/aaaaaaaa', { orgId: 1 }))
  })

  it('shows the OTLP endpoint and key beside the DSN with the tracing instructions', async () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <AppInstallationPage org="acme" site="desktop" orgId={1} siteId={19} publicId="6quqxuelthfz" timezone="UTC" />
      </QueryClientProvider>,
    )
    expect(await screen.findByLabelText('OTLP key')).toHaveTextContent(dsnKey.public_key)
    expect(screen.getByLabelText('OTLP endpoint')).toHaveTextContent(`${window.location.origin}/api/19/otlp`)
    expect(screen.getByRole('button', { name: 'Copy OTLP URL' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Copy OTLP key' })).toBeInTheDocument()
    expect(screen.getByText(/^OpenTelemetry: set/).parentElement).toHaveTextContent(
      `OpenTelemetry: set OTEL_EXPORTER_OTLP_ENDPOINT to this URL and OTEL_EXPORTER_OTLP_HEADERS to authorization=Bearer ${dsnKey.public_key}. Sentry SDKs: set tracesSampleRate to send transactions.`,
    )
  })

  it('builds the OTLP URL and environment snippet', () => {
    expect(otlpEndpoint('https://app.example.com', 19)).toBe('https://app.example.com/api/19/otlp')
    expect(otelEnvSnippet('https://app.example.com/api/19/otlp', 'k')).toContain('OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer k')
    expect(tracingSnippets().every((item) => item.code.toLowerCase().includes('sample'))).toBe(true)
  })

  it('lists a snippet for every supported SDK', () => {
    expect(sdkSnippets('https://k@h/1').map((sdk) => sdk.value)).toEqual(['browser', 'node', 'electron', 'python', 'go', 'android', 'cocoa'])
    for (const sdk of sdkSnippets('https://k@h/1')) expect(sdk.code).toContain('https://k@h/1')
  })
})
