import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('recharts', async (original) => ({
  ...(await original<typeof import('recharts')>()),
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}))

import { renderWithQuery } from '../testing'
import { ActiveUsersPage } from './ActiveUsersPage'
import { formatSeconds, formatStickiness, parseFeatureNames } from './api'
import { FeatureSets } from './FeatureSets'

const search = { period: '30d' as const }
const zero = '0001-01-01T00:00:00Z'
const adoptionRows = {
  release: [{ day: zero, value: '1.2.0', installs: 30, share: 0.75 }, { day: zero, value: '1.1.0', installs: 10, share: 0.25 }],
  platform: [{ day: zero, value: 'ios', installs: 25, share: 0.625 }],
  country: [{ day: zero, value: 'DE', installs: 12, share: 0.3 }],
  os: [{ day: zero, value: 'iOS 18', installs: 20, share: 0.5 }],
  device: [{ day: zero, value: 'phone', installs: 38, share: 0.95 }],
}

function mock(features: unknown[] = []) {
  privateRequest.mockImplementation((method: string, path: string) => {
    if (path.includes('/product/active-users')) {
      return Promise.resolve({
        series: [{ day: '2026-11-17T00:00:00Z', dau: 3, wau: 9, mau: 20 }, { day: '2026-11-18T00:00:00Z', dau: 4, wau: 10, mau: 22 }],
        distinct: 40, stickiness: 0.5, new_installs: [{ day: '2026-11-18T00:00:00Z', count: 2 }, { day: '2026-11-17T00:00:00Z', count: 3 }],
      })
    }
    if (path.includes('/product/engagement')) return Promise.resolve({ sessions: 120, sessions_per_user: 3, mean_session_seconds: 95, median_session_seconds: 40, screens_per_session: 4.25 })
    if (path.includes('/product/adoption')) {
      const by = new URL(path, 'http://x').searchParams.get('by') as keyof typeof adoptionRows
      return Promise.resolve({ by, series: [], totals: adoptionRows[by] })
    }
    if (path.includes('/adoption')) return Promise.resolve({ users: 5, active_users: 10, share: 0.5, series: [] })
    if (path.endsWith('/product/features') && method === 'GET') return Promise.resolve({ features })
    if (path.endsWith('/product/features') && method === 'POST') return Promise.resolve({ id: 1 })
    if (path.includes('/releases')) return Promise.resolve({ releases: [] })
    return Promise.resolve({})
  })
}

describe('ActiveUsersPage', () => {
  beforeEach(() => { vi.clearAllMocks(); mock() })
  afterEach(cleanup)

  it('shows DAU, WAU, MAU and stickiness as a percentage with the from/to range', async () => {
    renderWithQuery(<ActiveUsersPage siteId={7} timezone="UTC" search={search} onSearch={vi.fn()} canEdit />)
    const tiles = await screen.findByLabelText('Active installs')
    expect(within(tiles).getByText('DAU (latest day)').parentElement).toHaveTextContent('4')
    expect(within(tiles).getByText('WAU').parentElement).toHaveTextContent('10')
    expect(within(tiles).getByText('MAU').parentElement).toHaveTextContent('22')
    expect(within(tiles).getByText('Stickiness').parentElement).toHaveTextContent('50%')
    expect(within(tiles).getByText('New installs').parentElement).toHaveTextContent('5')
    expect(screen.getByRole('img', { name: 'Daily, weekly and monthly active installs' })).toBeInTheDocument()
    const call = privateRequest.mock.calls.find((item) => String(item[1]).includes('/product/active-users'))!
    expect(call[1]).toMatch(/\/api\/private\/sites\/7\/app\/product\/active-users\?from=\d{4}-\d{2}-\d{2}&to=\d{4}-\d{2}-\d{2}/)
  })

  it('shows engagement figures', async () => {
    renderWithQuery(<ActiveUsersPage siteId={7} timezone="UTC" search={search} onSearch={vi.fn()} canEdit />)
    const tiles = await screen.findByLabelText('Engagement')
    expect(tiles).toHaveTextContent('120')
    expect(tiles).toHaveTextContent('1m 35s')
    expect(tiles).toHaveTextContent('40s')
    expect(tiles).toHaveTextContent('4.3')
  })

  it('switches adoption between release, platform, country, OS and device with shares', async () => {
    renderWithQuery(<ActiveUsersPage siteId={7} timezone="UTC" search={search} onSearch={vi.fn()} canEdit />)
    const release = await screen.findByRole('table', { name: 'Adoption by release' })
    expect(release).toHaveTextContent('1.2.0')
    expect(release).toHaveTextContent('75.0%')
    for (const [tab, name, value, share] of [['Platform', 'platform', 'ios', '62.5%'], ['Country', 'country', 'DE', '30.0%'], ['OS', 'os', 'iOS 18', '50.0%'], ['Device', 'device', 'phone', '95.0%']]) {
      fireEvent.mouseDown(screen.getByRole('tab', { name: tab }))
      fireEvent.click(screen.getByRole('tab', { name: tab }))
      const table = await screen.findByRole('table', { name: `Adoption by ${name}` })
      expect(table).toHaveTextContent(value)
      expect(table).toHaveTextContent(share)
    }
  })
})

describe('FeatureSets', () => {
  beforeEach(() => { vi.clearAllMocks() })
  afterEach(cleanup)

  it('lists feature sets with their adoption share', async () => {
    mock([{ id: 1, name: 'Checkout', event_names: ['cart', 'pay'], created_at: '2026-11-01T00:00:00Z' }])
    renderWithQuery(<FeatureSets siteId={7} search={search} timezone="UTC" canEdit />)
    expect(await screen.findByText('Checkout')).toBeVisible()
    expect(await screen.findByLabelText('Checkout adoption')).toHaveTextContent('50.0% (5 of 10)')
  })

  it('validates 1 to 20 names before posting', async () => {
    mock()
    renderWithQuery(<FeatureSets siteId={7} search={search} timezone="UTC" canEdit />)
    fireEvent.change(await screen.findByLabelText('Feature name'), { target: { value: 'Checkout' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create feature set' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter at least one event name.')
    fireEvent.change(screen.getByLabelText('Event names'), { target: { value: Array.from({ length: 21 }, (_, i) => `e${i}`).join('\n') } })
    fireEvent.click(screen.getByRole('button', { name: 'Create feature set' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('at most 20')
    expect(privateRequest.mock.calls.some((item) => item[0] === 'POST')).toBe(false)
    fireEvent.change(screen.getByLabelText('Event names'), { target: { value: 'cart, pay\ncart' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create feature set' }))
    await waitFor(() => expect(privateRequest.mock.calls.some((item) => item[0] === 'POST')).toBe(true))
    const post = privateRequest.mock.calls.find((item) => item[0] === 'POST')!
    expect(post[2]).toEqual({ body: { name: 'Checkout', event_names: ['cart', 'pay'] } })
  })

  it('hides the form for viewers', async () => {
    mock()
    renderWithQuery(<FeatureSets siteId={7} search={search} timezone="UTC" canEdit={false} />)
    await screen.findByText('No feature sets yet.')
    expect(screen.queryByLabelText('Feature name')).not.toBeInTheDocument()
  })
})

describe('formatters', () => {
  it('formats stickiness, seconds and feature names', () => {
    expect(formatStickiness(0.5)).toBe('50%')
    expect(formatStickiness(null)).toBe('n/a')
    expect(formatSeconds(59.6)).toBe('1m 0s')
    expect(parseFeatureNames(' a ,b\n\n a').names).toEqual(['a', 'b'])
  })
})
