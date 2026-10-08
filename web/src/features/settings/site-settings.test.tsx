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
  })

  it('shows install guides, proxy advice, and a relative live status', () => {
    render(
      <InstallGuide
        publicId="site-public"
        lastEventReceivedAt="2026-10-07T09:59:40Z"
        now={new Date('2026-10-07T10:00:00Z')}
      />,
    )

    expect(screen.getByText('✓ Last event 20 seconds ago')).toBeVisible()
    expect(screen.getByText(/proxy the script through your own domain/i)).toBeVisible()
    expect(screen.getByText('Track custom clicks')).toBeVisible()
    expect(screen.getByText(/data-panel-event="Signup"/)).toBeVisible()
    const setup = screen.getByRole('region', { name: 'Feature setup' })
    expect(setup).toHaveTextContent(/Web Vitals/)
    expect(setup).toHaveTextContent(/panel\.track\('Purchase'/)
    expect(setup).toHaveTextContent(/currency: 'USD'/)
    expect(setup).toHaveTextContent(/panel\.identify\('user-123'/)
    expect(setup).toHaveTextContent(/name="panel:404"/)
    expect(setup).toHaveTextContent(/Site settings → Tracking/)
    expect(setup).toHaveTextContent(/Site settings → General/)
    expect(setup).toHaveTextContent(/panel\.optOut\(\)/)
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Next.js / React' }), {
      button: 0,
      ctrlKey: false,
    })
    expect(screen.getByText(/root layout/i)).toBeVisible()
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'WordPress / other CMS' }), {
      button: 0,
      ctrlKey: false,
    })
    expect(screen.getByText(/header injection/i)).toBeVisible()
  })

  it('polls every five seconds until the first event', () => {
    expect(installationStatusInterval(undefined)).toBe(5_000)
    expect(installationStatusInterval('2026-10-07T10:00:00Z')).toBe(false)
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
