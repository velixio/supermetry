import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
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
          mask: { value: true, configured: false, locked: true },
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
    expect(screen.getByText('Masking is required by the instance.')).toBeVisible()
    expect(screen.getByText('Configured masking: off. Effective masking: on.')).toBeVisible()
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

  it('requires instance permission before a project can enable replay', () => {
    const onSave = vi.fn()
    render(<ReplaySettingsForm settings={{ ...settings(false), configured_enabled: false, instance_enabled: false, restrictions: ['instance_replay_disabled'] }} saving={false} onSave={onSave} />)
    expect(screen.getByRole('switch', { name: 'Enable session replay for this project' })).toBeDisabled()
    expect(screen.getByText(/Instance replay is off/)).toBeVisible()
  })

  it('lets a permitted project explicitly opt in and sends that choice', () => {
    const onSave = vi.fn()
    render(<ReplaySettingsForm settings={{ ...settings(false), configured_enabled: false, instance_enabled: true }} saving={false} onSave={onSave} />)
    fireEvent.click(screen.getByRole('switch', { name: 'Enable session replay for this project' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save replay settings' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ configured_enabled: true }))
  })

  it('allows tighter values under an instance cap and shows configured versus effective values', () => {
    const onSave = vi.fn()
    render(<ReplaySettingsForm settings={{ ...settings(true), configured_enabled: true, instance_enabled: true, sample_rate: { value: 10, configured: 40, locked: true, maximum: 10, restriction: 'instance_sample_rate_cap' } }} saving={false} onSave={onSave} />)
    expect(screen.getByRole('spinbutton', { name: 'Sample rate (%)' })).toBeEnabled()
    expect(screen.getByRole('spinbutton', { name: 'Sample rate (%)' })).toHaveAttribute('max', '10')
    expect(screen.getByText('Configured: 40. Effective: 10.')).toBeVisible()
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Sample rate (%)' }), { target: { value: '11' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save replay settings' }))
    expect(onSave).not.toHaveBeenCalled()
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Sample rate (%)' }), { target: { value: '5' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save replay settings' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ sample_rate: expect.objectContaining({ value: 5 }) }))
  })

  it('allows withdrawing a stored project opt-in after instance replay is disabled', () => {
    const onSave = vi.fn()
    render(<ReplaySettingsForm settings={{ ...settings(false), configured_enabled: true, instance_enabled: false }} saving={false} onSave={onSave} />)
    fireEvent.click(screen.getByRole('switch', { name: 'Enable session replay for this project' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save replay settings' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ configured_enabled: false }))
    expect(screen.getByRole('switch', { name: 'Enable session replay for this project' })).toBeDisabled()
  })

  it('disables inactive triggers without rewriting stored settings', () => {
    const onSave = vi.fn()
    const stored = { ...settings(true), configured_enabled: true, instance_enabled: true }
    render(<ReplaySettingsForm settings={stored} saving={false} onSave={onSave} />)
    for (const name of ['JavaScript errors', 'Rage clicks', 'Matching URLs', 'Manual trigger']) {
      expect(screen.getByRole('switch', { name })).toBeDisabled()
      fireEvent.click(screen.getByRole('switch', { name }))
    }
    expect(screen.getByRole('textbox', { name: 'URL patterns' })).toBeDisabled()
    expect(screen.getByText(/Triggered capture is unavailable under the sampling ceiling/)).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Save replay settings' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ triggers: stored.triggers }))
  })

  it('keeps settings read-only without edit permission', () => {
    render(<ReplaySettingsForm settings={{ ...settings(true), configured_enabled: true, instance_enabled: true }} editable={false} saving={false} onSave={vi.fn()} />)
    for (const control of screen.getAllByRole('switch')) expect(control).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Save replay settings' })).not.toBeInTheDocument()
  })
})
