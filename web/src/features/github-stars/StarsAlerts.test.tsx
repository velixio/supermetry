import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@tanstack/react-router')>()),
  Link: ({ to, params, children, ...rest }: { to: string; params?: Record<string, string>; children: React.ReactNode }) => (
    <a href={to.replace(/\$(\w+)/g, (_m, key: string) => params?.[key] ?? '')} {...rest}>{children}</a>
  ),
}))

const api = vi.hoisted(() => ({ useStarsAlerts: vi.fn(), useSaveStarsAlerts: vi.fn(), useTestStarsAlert: vi.fn(), useChannels: vi.fn() }))
vi.mock('./alertsApi', () => api)
vi.mock('@/features/settings/notificationApi', () => ({ useChannels: api.useChannels }))

import { StarsAlerts } from './StarsAlerts'
import { StarsOrgProvider } from './starsOrg'

const kinds = [
  { kind: 'star_milestone', label: 'Star milestones', description: 'Round counts.' },
  { kind: 'star_spike', label: 'Star spikes', description: 'Fast growth.' },
]
const mutation = (over: Record<string, unknown> = {}) => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false, error: null, data: undefined, ...over })

function setup(opts: { canManage?: boolean; channels?: unknown[]; settings?: unknown; save?: ReturnType<typeof mutation>; test?: ReturnType<typeof mutation> } = {}) {
  const save = opts.save ?? mutation()
  const test = opts.test ?? mutation()
  api.useStarsAlerts.mockReturnValue({
    isPending: false, error: null,
    data: opts.settings ?? { events: { star_milestone: true, star_spike: false }, channel_ids: [7], event_kinds: kinds },
  })
  api.useChannels.mockReturnValue({
    isPending: false,
    data: { data: opts.channels ?? [{ id: 7, kind: 'telegram', name: 'Ops chat', enabled: true }, { id: 8, kind: 'email', name: 'Owners', enabled: true }] },
  })
  api.useSaveStarsAlerts.mockReturnValue(save)
  api.useTestStarsAlert.mockReturnValue(test)
  render(<StarsOrgProvider value={{ orgId: 3, canManage: opts.canManage ?? true }}><StarsAlerts org="acme" repoId={5} /></StarsOrgProvider>)
  return { save, test }
}

describe('StarsAlerts', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(cleanup)

  it('shows the saved events and channels and saves the edited selection', () => {
    const { save } = setup()
    expect(screen.getByRole('checkbox', { name: 'Star milestones' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Star spikes' })).not.toBeChecked()
    expect(screen.getByRole('checkbox', { name: /Ops chat/ })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: /Owners/ })).not.toBeChecked()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Star spikes' }))
    fireEvent.click(screen.getByRole('checkbox', { name: /Owners/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Save alerts' }))
    expect(save.mutate).toHaveBeenCalledWith({ events: { star_milestone: true, star_spike: true }, channel_ids: [7, 8] }, expect.anything())
  })

  it('links to the notification channel settings', () => {
    setup()
    expect(screen.getByRole('link', { name: 'Add or edit notification channels' })).toHaveAttribute('href', '/app/acme/settings/notifications')
  })

  it('tells the user when the organization has no channels', () => {
    setup({ channels: [], settings: { events: {}, channel_ids: [], event_kinds: kinds } })
    expect(screen.getByText('This organization has no notification channels yet.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Send test alert' })).toBeDisabled()
  })

  it('sends a test alert and reports each channel, including a failure', () => {
    const { test } = setup()
    fireEvent.click(screen.getByRole('button', { name: 'Send test alert' }))
    expect(test.mutate).toHaveBeenCalled()
    cleanup()
    setup({ test: mutation({ data: { data: [{ channel_id: 7, ok: true }, { channel_id: 8, ok: false, error: 'email delivery is disabled' }] } }) })
    expect(screen.getByText('Test alert sent to Ops chat.')).toBeInTheDocument()
    expect(screen.getByText('Could not send to Owners: email delivery is disabled.')).toBeInTheDocument()
  })

  it('is view-only for members', () => {
    setup({ canManage: false })
    expect(screen.getByRole('checkbox', { name: 'Star milestones' })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: /Ops chat/ })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Save alerts' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Send test alert' })).not.toBeInTheDocument()
    expect(screen.getByText('Only owners and admins can change alerts.')).toBeInTheDocument()
  })
})
