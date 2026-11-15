import '@testing-library/jest-dom/vitest'

import { act, cleanup, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { TestReportCheck, testReportSnippet } from './TestReportCheck'
import { renderWithQuery } from './testing'

const overview = (events: number) => ({ events, crashes: 0, new_issues: 0, regressed_issues: 0, crash_free_users: null, crash_free_sessions: null, series: [] })
const calls = () => privateRequest.mock.calls.filter(([, path]) => String(path).includes('/overview')).length

describe('TestReportCheck', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.clearAllMocks() })
  afterEach(() => { cleanup(); vi.useRealTimers() })

  it('builds a curl snippet with the app id and a placeholder key', () => {
    const snippet = testReportSnippet('https://sm.example', 'pub7')
    expect(snippet).toContain('curl --request POST https://sm.example/api/public/v1/app-events')
    expect(snippet).toContain("'X-Supermetry-Key: <your key>'")
    expect(snippet).toContain('"site":"pub7"')
    expect(snippet).toContain('"kind":"error"')
  })

  it('lists the three steps and polls every 5 seconds until a report arrives', async () => {
    privateRequest.mockResolvedValue(overview(0))
    renderWithQuery(<TestReportCheck appId="pub7" siteId={7} timezone="UTC" origin="https://sm.example" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText(/Add the key to your build/)).toBeInTheDocument()
    expect(screen.getByText(/Send a test report/)).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Waiting for first report…')
    const before = calls()
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(calls()).toBe(before + 1)
    privateRequest.mockResolvedValue(overview(1))
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    await act(async () => { await vi.advanceTimersByTimeAsync(10) })
    expect(screen.getByRole('status')).toHaveTextContent('First report received')
    expect(screen.getByRole('status')).toHaveAttribute('data-state', 'received')
    const settled = calls()
    await act(async () => { await vi.advanceTimersByTimeAsync(20000) })
    expect(calls()).toBe(settled)
  })

  it('stops polling after two minutes', async () => {
    privateRequest.mockResolvedValue(overview(0))
    renderWithQuery(<TestReportCheck appId="pub7" siteId={7} timezone="UTC" origin="https://sm.example" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(121_000) })
    expect(screen.getByRole('status')).toHaveTextContent('No report yet')
    const settled = calls()
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(calls()).toBe(settled)
  })
})
