import '@testing-library/jest-dom/vitest'

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { toast } from 'sonner'

const { refetchSiteIcon } = vi.hoisted(() => ({ refetchSiteIcon: vi.fn() }))

vi.mock('@/features/sites/api', () => ({ refetchSiteIcon }))
vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    info: vi.fn(),
    success: vi.fn(),
  },
}))

import {
  InstallGuide,
  SiteIconSettings,
  SiteSettingsTabs,
  installationStatusInterval,
} from './SiteSettings'
import { renderWithRouter } from '@/test/renderWithRouter'

describe('site settings', () => {
  beforeEach(() => {
    refetchSiteIcon.mockReset()
  })
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  function renderIconSettings(role: 'owner' | 'admin' | 'editor' | 'viewer' = 'owner') {
    const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
    queryClient.setQueryData(['site', 7], { id: 7, name: 'Docs', icon_url: '' })
    queryClient.setQueryData(['me'], {
      memberships: [{ sites: [{ id: 7, name: 'Docs', icon_url: '' }] }],
    })
    render(
      <QueryClientProvider client={queryClient}>
        <SiteIconSettings
          iconUrl=""
          name="Docs"
          orgId={3}
          role={role}
          siteId={7}
        />
      </QueryClientProvider>,
    )
    return queryClient
  }

  it('shows all settings tabs including Tracking', async () => {
    renderWithRouter(<SiteSettingsTabs org="acme" site="docs" current="installation" />)
    for (const label of ['General', 'Installation', 'Tracking', 'Goals', 'Funnels', 'Shares', 'Reports', 'Alerts', 'Imports & export']) {
      expect(await screen.findByRole('link', { name: label })).toBeVisible()
    }
    expect(screen.getByRole('link', { name: 'Installation' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    expect(screen.getByRole('navigation', { name: 'Site settings' })).toBeVisible()
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument()
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
  })

  it('shows install guides, proxy advice, and a receiving status', () => {
    render(
      <InstallGuide
        publicId="site-public"
        installStatus="receiving"
        lastSeenAt="2026-10-07T09:59:40Z"
        now={new Date('2026-10-07T10:00:00Z')}
      />,
    )

    expect(screen.getByText('Receiving')).toBeVisible()
    expect(screen.getByText('Last event 20 seconds ago')).toBeVisible()
    expect(screen.getByText(/proxy the script through your own domain/i)).toBeVisible()
    expect(screen.getByText('Track custom clicks')).toBeVisible()
    expect(screen.getByText(/data-supermetry-event="Signup"/)).toBeVisible()
    const setup = screen.getByRole('region', { name: 'Feature setup' })
    expect(setup).toHaveTextContent(/Web Vitals/)
    expect(setup).toHaveTextContent(/supermetry\.track\('Purchase'/)
    expect(setup).toHaveTextContent(/currency: 'USD'/)
    expect(setup).toHaveTextContent(/supermetry\.identify\('user-123'/)
    expect(setup).toHaveTextContent(/name="supermetry:404"/)
    expect(setup).toHaveTextContent(/Site settings → Tracking/)
    expect(setup).toHaveTextContent(/Site settings → General/)
    expect(setup).toHaveTextContent(/supermetry\.optOut\(\)/)
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Next.js' }), { button: 0, ctrlKey: false })
    expect(screen.getByText('app/layout.tsx')).toBeVisible()
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'WordPress' }), { button: 0, ctrlKey: false })
    expect(screen.getByText(/functions\.php/i)).toBeVisible()
  })

  it.each([
    ['waiting', undefined, 'Waiting', 'Waiting for the first accepted event.'],
    ['receiving', '2026-10-07T09:59:40Z', 'Receiving', 'Last event 20 seconds ago'],
    ['quiet', '2026-10-06T09:00:00Z', 'Quiet', 'Last event 1 day ago'],
  ] as const)('shows the %s install state', (installStatus, lastSeenAt, label, detail) => {
    render(<InstallGuide publicId="site-public" installStatus={installStatus} lastSeenAt={lastSeenAt} now={new Date('2026-10-07T10:00:00Z')} />)
    expect(screen.getByText(label)).toBeVisible()
    expect(screen.getByText(detail)).toBeVisible()
  })

  it('polls every few seconds until the first event, then every thirty seconds', () => {
    expect(installationStatusInterval(undefined)).toBe(5_000)
    expect(installationStatusInterval('waiting')).toBe(5_000)
    expect(installationStatusInterval('receiving')).toBe(30_000)
    expect(installationStatusInterval('quiet')).toBe(30_000)
  })

  it('puts the live status first, with troubleshooting while waiting', () => {
    render(<InstallGuide publicId="site-public" installStatus="waiting" snippet="<script></script>" fullSnippet="<script></script>" />)
    const heading = screen.getByRole('heading', { name: 'Waiting for the first event' })
    const snippetButton = screen.getByRole('button', { name: 'Copy tracking snippet' })
    expect(heading.compareDocumentPosition(snippetButton) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByRole('list', { name: 'Setup progress' })).toHaveTextContent('Install the snippet')
    expect(screen.getByText('No event yet? Check these common causes')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Open dashboard' })).not.toBeInTheDocument()
  })

  it('offers the dashboard once events arrive', async () => {
    renderWithRouter(<InstallGuide publicId="site-public" installStatus="receiving" lastSeenAt="2026-10-07T09:59:40Z" now={new Date('2026-10-07T10:00:00Z')} dashboardHref="/app/acme/docs" domainsHref="/app/acme/docs/settings/general" snippet="<script></script>" fullSnippet="<script></script>" />)
    expect(await screen.findByRole('link', { name: 'Open dashboard' })).toHaveAttribute('href', '/app/acme/docs')
    expect(screen.getByRole('heading', { name: 'Events are arriving' })).toBeVisible()
    expect(screen.queryByText('No event yet? Check these common causes')).not.toBeInTheDocument()
  })

  it('holds the snippet space while it loads and offers a retry when it fails', () => {
    const { rerender } = render(<InstallGuide publicId="site-public" snippetLoading />)
    expect(screen.getByRole('status', { name: 'Loading your snippet…' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Copy tracking snippet' })).not.toBeInTheDocument()
    const onRetry = vi.fn()
    rerender(<InstallGuide publicId="site-public" snippetFailed onRetrySnippet={onRetry} />)
    expect(screen.getByText('Your tracking snippet could not be loaded.')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalled()
  })

  it('updates the icon cache and confirms a successful refetch', async () => {
    const updatedSite = {
      id: 7,
      name: 'Docs',
      icon_url: 'https://docs.example/icon.png',
    }
    let resolveRefetch!: (site: typeof updatedSite) => void
    refetchSiteIcon.mockImplementation(() => new Promise((resolve) => {
      resolveRefetch = resolve
    }))
    const queryClient = renderIconSettings()

    fireEvent.click(screen.getByRole('button', { name: 'Refetch' }))

    await waitFor(() => expect(screen.getByRole('button', { name: 'Refetching…' })).toBeDisabled())
    await act(async () => resolveRefetch(updatedSite))
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Icon updated'))
    expect(queryClient.getQueryData(['site', 7])).toEqual(
      expect.objectContaining({ icon_url: 'https://docs.example/icon.png' }),
    )
    expect(queryClient.getQueryData<{ memberships: { sites: { id: number; icon_url: string }[] }[] }>(['me'])
      ?.memberships[0].sites[0].icon_url).toBe('https://docs.example/icon.png')
  })

  it('reports when refetching finds no icon', async () => {
    refetchSiteIcon.mockResolvedValue({ id: 7, name: 'Docs', icon_url: '' })
    renderIconSettings('admin')

    fireEvent.click(screen.getByRole('button', { name: 'Refetch' }))

    await waitFor(() => expect(toast.info).toHaveBeenCalledWith('No icon found'))
  })

  it('shows a specific message when refetching is rate limited', async () => {
    refetchSiteIcon.mockRejectedValue(Object.assign(new Error('rate limited'), { status: 429 }))
    renderIconSettings()

    fireEvent.click(screen.getByRole('button', { name: 'Refetch' }))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Please wait a few seconds'))
  })

  it.each(['editor', 'viewer'] as const)('hides refetch from %s members', (role) => {
    renderIconSettings(role)

    expect(screen.queryByRole('button', { name: 'Refetch' })).not.toBeInTheDocument()
    expect(screen.getByText("Fetched from your site's pages")).toBeVisible()
  })
})
