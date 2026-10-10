import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { deleteSite, getSite } = vi.hoisted(() => ({ deleteSite: vi.fn(), getSite: vi.fn() }))
const { heartbeatDemo } = vi.hoisted(() => ({ heartbeatDemo: vi.fn() }))
const { navigate } = vi.hoisted(() => ({ navigate: vi.fn() }))
vi.mock('@/features/sites/api', () => ({ deleteSite, getSite }))
vi.mock('./api', () => ({ heartbeatDemo }))
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => navigate }))

import { DemoBanner, demoSiteRefetchInterval } from './DemoBanner'

const org = { id: 1, slug: 'acme' }
const demo: {
  id: number
  name: string
  public_id: string
  is_demo: boolean
  demo_ready: boolean
  demo_expires_at?: string
} = { id: 7, name: 'Demo project', public_id: 'demo-public', is_demo: true, demo_ready: false }

function renderBanner(site = demo) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
      <DemoBanner org={org} site={site} />
    </QueryClientProvider>,
  )
}

describe('DemoBanner', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getSite.mockResolvedValue(demo)
    deleteSite.mockResolvedValue(undefined)
    heartbeatDemo.mockResolvedValue(undefined)
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  })
  afterEach(() => {
    cleanup()
    vi.useRealTimers()
  })

  it('shows generation progress and polls until the demo is ready', () => {
    renderBanner()
    expect(screen.getByText('Generating demo data…')).toBeVisible()
    expect(demoSiteRefetchInterval({ state: { data: demo } })).toBe(2_000)
    expect(demoSiteRefetchInterval({ state: { data: { ...demo, demo_ready: true } } })).toBe(false)
  })

  it('shows a terminal failure state and stops polling', () => {
    const failed = { ...demo, demo_failed: true }
    renderBanner(failed)
    expect(screen.getByText('Demo data could not be generated.')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Remove demo project' })).toBeVisible()
    expect(demoSiteRefetchInterval({ state: { data: failed } })).toBe(false)
  })

  it('shows the ready state and removes the project only after confirmation', async () => {
    renderBanner({ ...demo, demo_ready: true })
    expect(screen.getByText("You're viewing demo data")).toBeVisible()

    fireEvent.click(screen.getByRole('button', { name: 'Remove demo project' }))
    expect(deleteSite).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Remove demo' }))
    await waitFor(() => expect(deleteSite).toHaveBeenCalledWith(7, 1))
  })

  it('shows the remaining lifetime using hours and minutes', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-11T12:00:00Z'))
    renderBanner({ ...demo, demo_ready: true, demo_expires_at: '2026-10-11T14:31:00Z' })
    expect(screen.getByText('Demo expires in 2h 31m')).toBeVisible()
    cleanup()
    renderBanner({ ...demo, demo_ready: true, demo_expires_at: '2026-10-11T12:42:00Z' })
    expect(screen.getByText('Demo expires in 42m')).toBeVisible()
  })

  it('refetches at expiry and redirects after the expired site returns 404', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-11T11:59:59Z'))
    const expiring = { ...demo, demo_ready: true, demo_expires_at: '2026-10-11T12:00:00Z' }
    getSite
      .mockResolvedValueOnce(expiring)
      .mockRejectedValueOnce(Object.assign(new Error('not found'), { status: 404 }))

    renderBanner(expiring)
    await act(async () => { await Promise.resolve() })
    expect(getSite).toHaveBeenCalledTimes(1)

    await act(async () => { await vi.advanceTimersByTimeAsync(1_000) })
    expect(getSite).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(navigate).toHaveBeenCalledWith({ to: '/app/$org', params: { org: 'acme' } })
  })

  it('heartbeats while visible and stops while hidden or unmounted', async () => {
    vi.useFakeTimers()
    const view = renderBanner({ ...demo, demo_ready: true })
    expect(heartbeatDemo).toHaveBeenCalledTimes(1)
    expect(heartbeatDemo).toHaveBeenLastCalledWith(7)

    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(heartbeatDemo).toHaveBeenCalledTimes(2)

    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    act(() => document.dispatchEvent(new Event('visibilitychange')))
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(heartbeatDemo).toHaveBeenCalledTimes(2)

    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
    act(() => document.dispatchEvent(new Event('visibilitychange')))
    expect(heartbeatDemo).toHaveBeenCalledTimes(3)
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(heartbeatDemo).toHaveBeenCalledTimes(4)

    view.unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(heartbeatDemo).toHaveBeenCalledTimes(4)
  })

  it('stops heartbeats after the demo is not found', async () => {
    vi.useFakeTimers()
    heartbeatDemo.mockRejectedValue(Object.assign(new Error('not found'), { status: 404 }))
    renderBanner({ ...demo, demo_ready: true })
    await act(async () => { await Promise.resolve() })
    expect(heartbeatDemo).toHaveBeenCalledTimes(1)

    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    act(() => document.dispatchEvent(new Event('visibilitychange')))
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
    act(() => document.dispatchEvent(new Event('visibilitychange')))
    expect(heartbeatDemo).toHaveBeenCalledTimes(1)
  })
})
