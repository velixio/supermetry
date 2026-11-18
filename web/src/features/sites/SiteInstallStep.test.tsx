import '@testing-library/jest-dom/vitest'

import { QueryClient } from '@tanstack/react-query'
import { act, cleanup, fireEvent, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest, copyText } = vi.hoisted(() => ({ privateRequest: vi.fn(), copyText: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('@/lib/clipboard', () => ({ copyText }))

import { renderWithRouter } from '@/features/onboarding/testUtils'

import { installPollInterval, SiteInstallStep } from './SiteInstallStep'

const snippet = { snippet: '<script defer data-site="pub_1" src="https://stats.example.com/js/script.js"></script>', full_snippet: '' }
let installStatus: 'waiting' | 'receiving' = 'waiting'

function mount() {
  return renderWithRouter(
    <SiteInstallStep orgId={1} siteId={7} publicId="pub_1" dashboardHref="/app/acme/example" />,
    new QueryClient({ defaultOptions: { queries: { retry: false } } }),
  )
}

beforeEach(() => {
  installStatus = 'waiting'
  copyText.mockResolvedValue(true)
  privateRequest.mockImplementation((_method: string, path: string) =>
    Promise.resolve(path.endsWith('/snippet') ? snippet : { id: 7, install_status: installStatus }))
})
afterEach(() => { cleanup(); vi.useRealTimers(); vi.clearAllMocks() })

describe('SiteInstallStep', () => {
  it('shows framework tabs and switches the snippet', async () => {
    await mount()
    const tabs = await screen.findByRole('tablist', { name: 'Installation method' })
    expect(Array.from(tabs.querySelectorAll('[role=tab]')).map((tab) => tab.textContent)).toEqual(
      expect.arrayContaining(['HTML', 'Next.js', 'Astro', 'WordPress', 'Google Tag Manager']),
    )
    expect(await screen.findByLabelText('HTML snippet')).toHaveTextContent('data-site="pub_1"')
    fireEvent.click(screen.getByRole('tab', { name: 'Next.js' }))
    expect(screen.getByLabelText('Next.js snippet')).toBeInTheDocument()
    expect(screen.getByLabelText('Next.js install command')).toHaveTextContent('npm install @supermetry/nextjs')
  })

  it('copies the snippet and announces it in a live region', async () => {
    await mount()
    fireEvent.click(await screen.findByRole('button', { name: 'Copy snippet' }))
    await act(async () => { await Promise.resolve() })
    expect(copyText).toHaveBeenCalledWith(expect.stringContaining('data-site="pub_1"'))
    const live = (await screen.findAllByText('Copied'))[0]
    expect(live).toHaveAttribute('role', 'status')
    expect(live).toHaveAttribute('aria-live', 'polite')
  })

  it('polls while waiting, then turns into a success state and stops', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    await mount()
    expect(await screen.findByText('Waiting for the first event…')).toBeInTheDocument()
    expect(screen.getByRole('status', { name: 'Installation status' })).toHaveAttribute('aria-live', 'polite')
    const statusCalls = () => privateRequest.mock.calls.filter(([, path]) => path === '/api/private/sites/7').length
    expect(statusCalls()).toBe(1)

    installStatus = 'receiving'
    await act(async () => { await vi.advanceTimersByTimeAsync(3_100) })
    expect(statusCalls()).toBe(2)
    expect(await screen.findByText('First event received')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Go to dashboard' })).toHaveAttribute('href', '/app/acme/example')

    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(statusCalls()).toBe(2)
  })

  it('polls only until an event is in', () => {
    expect(installPollInterval(undefined)).toBe(3_000)
    expect(installPollInterval('waiting')).toBe(3_000)
    expect(installPollInterval('receiving')).toBe(false)
    expect(installPollInterval('quiet')).toBe(false)
  })
})
