import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => <a href={to.replace('$org', params.org).replace('$site', params.site)}>{children}</a>,
}))
vi.mock('@/features/settings/privateApi', () => ({
  privateRequest: vi.fn(async () => ({
    keys: [
      { prefix: 'abcd1234', created_at: '2026-11-15T00:00:00Z', expires_at: null },
      { prefix: 'old00000', created_at: '2026-11-01T00:00:00Z', expires_at: '2026-11-22T00:00:00Z' },
    ],
  })),
}))
vi.mock('./TestReportCheck', () => ({ TestReportCheck: ({ appId }: { appId: string }) => <p>check for {appId}</p> }))

import { AppInstallationPage, appInstallSnippets } from './AppInstallationPage'

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
    expect(screen.getByLabelText('App ID')).toHaveTextContent('6quqxuelthfz')
    expect(screen.getByLabelText('Host')).toHaveTextContent(window.location.origin)
    expect(await screen.findByText('smk_abcd1234…')).toBeInTheDocument()
    // After a rotation the old key is listed with its end date.
    expect(screen.getByText('smk_old00000…')).toBeInTheDocument()
    expect(screen.getByText(`valid until ${new Date('2026-11-22T00:00:00Z').toLocaleDateString()}`)).toBeInTheDocument()
    expect(screen.getAllByText(/^valid until/)).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'Rotate the key in App settings' })).toHaveAttribute('href', '/app/acme/desktop/settings/app')
    expect(screen.getByText('check for 6quqxuelthfz')).toBeInTheDocument()
  })
})
