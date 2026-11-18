import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ProfilesSettings } from './ProfilesSettings'

const warning = 'Identifies individual users. Only enable this if your users have consented to analytics that can identify them.'

afterEach(cleanup)

describe('ProfilesSettings', () => {
  it('explains how profiles affect collection without changing the setting on render', async () => {
    const onChange = vi.fn()
    render(<ProfilesSettings enabled={false} onChange={onChange} role="owner" policy={{ instance_mode: 'identified', configured_profiles_enabled: false, effective_mode: 'anonymous', restrictions: ['project_profiles_disabled'] }} />)

    fireEvent.click(screen.getByRole('button', { name: 'About profiles' }))
    expect((await screen.findAllByText(/controls this project's effective collection mode/i)).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/stored as \/\[redacted\]/i).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/permits identifiable data and original paths from new events to be stored/i).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/previously redacted paths cannot be restored/i).length).toBeGreaterThan(0)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('shows configured opt-in but blocks profiles under anonymous instance policy', () => {
    const onChange = vi.fn()
    render(<ProfilesSettings enabled={true} onChange={onChange} role="owner" policy={{ instance_mode: 'anonymous', configured_profiles_enabled: true, effective_mode: 'anonymous', restrictions: ['instance_anonymous'] }} />)
    expect(screen.getByRole('switch', { name: 'Enable profiles' })).toBeDisabled()
    expect(screen.getByText(/Tracking: anonymous/)).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'About profiles' }))
    expect(screen.getAllByText(/Historical identity access is disabled/).length).toBeGreaterThan(0)
    fireEvent.click(screen.getByRole('switch', { name: 'Enable profiles' }))
    expect(onChange).not.toHaveBeenCalled()
  })

  it('shows the exact consent warning and lets owners enable profiles', () => {
    const onChange = vi.fn()
    render(<ProfilesSettings enabled={false} onChange={onChange} role="owner" policy={{ instance_mode: 'identified', configured_profiles_enabled: false, effective_mode: 'anonymous', restrictions: ['project_profiles_disabled'] }} />)

    expect(screen.getByText(warning)).toBeVisible()
    fireEvent.click(screen.getByRole('switch', { name: 'Enable profiles' }))
    expect(onChange).toHaveBeenCalledWith(true)
  })

  it('does not enable profiles when instance policy is unavailable', () => {
    render(<ProfilesSettings enabled={false} onChange={vi.fn()} role="owner" />)
    expect(screen.getByRole('switch', { name: 'Enable profiles' })).toBeDisabled()
    expect(screen.getByText(/Tracking policy unavailable/)).toBeVisible()
  })

  it('makes profile enablement read-only for editors', () => {
    render(<ProfilesSettings enabled={false} onChange={() => undefined} role="editor" />)

    expect(screen.getByRole('switch', { name: 'Enable profiles' })).toBeDisabled()
    expect(screen.getByText(/only owners and admins can change this/i)).toBeVisible()
  })
})
