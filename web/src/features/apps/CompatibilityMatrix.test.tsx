import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => <a href={Object.entries(params).reduce((href, [key, value]) => href.replace('$' + key, value), to)}>{children}</a>,
}))

import { CompatibilityMatrix, crashFreeTone } from './CompatibilityMatrix'
import { renderWithQuery } from './testing'

const rows = [
  { platform: 'windows', os_version: '11', arch: 'x64', device_class: 'desktop', events: 500, crashes: 5, installs: 200, crash_free_users: 0.995, top_issues: [{ id: 11, title: 'Segfault in renderer', events: 4 }] },
  { platform: 'macos', os_version: '14', arch: 'arm64', device_class: 'laptop', events: 90, crashes: 30, installs: 40, crash_free_users: 0.8, top_issues: [] },
  { platform: 'linux', os_version: '6', arch: 'x64', device_class: 'desktop', events: 3, crashes: 0, installs: 0, crash_free_users: null, top_issues: [] },
]
const props = { org: 'acme', site: 'desk', siteId: 7, timezone: 'UTC' }

describe('CompatibilityMatrix', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockImplementation((_m: string, path: string) => Promise.resolve(path.includes('/compatibility') ? { rows } : { releases: [] }))
  })
  afterEach(cleanup)

  it('maps crash-free rates to tones', () => {
    expect(crashFreeTone(0.999)).toBe('good')
    expect(crashFreeTone(0.97)).toBe('warn')
    expect(crashFreeTone(0.8)).toBe('bad')
    expect(crashFreeTone(null)).toBe('none')
  })

  it('renders a row per combination with the crash-free rate and linked top issues', async () => {
    renderWithQuery(<CompatibilityMatrix {...props} search={{ period: '30d' }} onSearch={vi.fn()} />)
    const row = (await screen.findByText('11')).closest('tr')!
    expect(row).toHaveTextContent('windows')
    expect(row).toHaveTextContent('x64')
    expect(row).toHaveTextContent('99.5%')
    expect(row.querySelector('[data-tone="good"]')).not.toBeNull()
    expect(screen.getByText('14').closest('tr')!.querySelector('[data-tone="bad"]')).not.toBeNull()
    expect(screen.getByRole('link', { name: 'Segfault in renderer' })).toHaveAttribute('href', '/app/acme/desk/issues/11')
    expect(screen.getAllByText('n/a').length).toBeGreaterThan(0)
  })

  it('filters by platform with chips', async () => {
    const onSearch = vi.fn()
    renderWithQuery(<CompatibilityMatrix {...props} search={{ period: '30d' }} onSearch={onSearch} />)
    await screen.findByText('11')
    fireEvent.click(screen.getByRole('button', { name: 'macos' }))
    expect(onSearch).toHaveBeenCalledWith({ period: '30d', platform: 'macos' })
    fireEvent.click(screen.getByRole('button', { name: 'All platforms' }))
    expect(onSearch).toHaveBeenLastCalledWith({ period: '30d', platform: undefined })
  })
})
