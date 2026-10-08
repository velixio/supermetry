import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ReplaySettingsForm } from './ReplaySettingsForm'

afterEach(cleanup)
beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})

describe('ReplaySettingsForm', () => {
  it('disables locked fields and explains that the environment controls them', () => {
    render(
      <ReplaySettingsForm
        settings={{
          enabled: true,
          mask: { value: true, locked: true },
          sample_rate: { value: 15, locked: false },
          retention_days: { value: 30, locked: false },
          quota_bytes: { value: 5_000_000_000, locked: false },
          triggers: { error: true, rage_click: true, url: false, manual: true, url_patterns: ['/checkout/*'] },
          consent: 'auto',
        }}
        saving={false}
        onSave={vi.fn()}
      />,
    )

    expect(screen.getByRole('switch', { name: 'Mask page content' })).toBeDisabled()
    expect(screen.getByText('Set by environment variable')).toBeVisible()
  })

  const settings = (enabled: boolean) => ({
    enabled,
    mask: { value: false, locked: false },
    sample_rate: { value: 15, locked: false },
    retention_days: { value: 30, locked: false },
    quota_bytes: { value: 5_000_000_000, locked: false },
    triggers: { error: true, rage_click: true, url: true, manual: true, url_patterns: [] },
    consent: 'auto' as const,
  })

  it('shows the environment switch as read-only status when replay is on', () => {
    render(<ReplaySettingsForm settings={settings(true)} saving={false} onSave={vi.fn()} />)
    expect(screen.queryByRole('switch', { name: 'Enable session replay' })).not.toBeInTheDocument()
    expect(screen.getAllByText(/^On/)[0]).toBeVisible()
    expect(screen.getAllByText('PANEL_REPLAY_ENABLED')[0]).toBeVisible()
  })

  it('tells the admin how to turn replay on when it is off', () => {
    render(<ReplaySettingsForm settings={settings(false)} saving={false} onSave={vi.fn()} />)
    expect(screen.queryByRole('switch', { name: 'Enable session replay' })).not.toBeInTheDocument()
    expect(screen.getAllByText(/^Off/)[0]).toBeVisible()
    expect(screen.getByText('PANEL_REPLAY_ENABLED=true')).toBeVisible()
  })
})
