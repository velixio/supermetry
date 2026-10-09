import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ProfilesSettings } from './ProfilesSettings'

const warning = 'Identifies individual users. Only enable this if your users have consented to analytics that can identify them.'

afterEach(cleanup)

describe('ProfilesSettings', () => {
  it('shows configured opt-in but blocks profiles under anonymous instance policy', () => {
    const onChange = vi.fn()
    render(<ProfilesSettings enabled={true} onChange={onChange} role="owner" policy={{ instance_mode: 'anonymous', configured_profiles_enabled: true, effective_mode: 'anonymous', restrictions: ['instance_anonymous'] }} />)
    expect(screen.getByRole('switch', { name: 'Enable profiles' })).toBeDisabled()
    expect(screen.getByText(/Effective tracking: anonymous/)).toBeVisible()
    expect(screen.getByText(/Historical identity access is disabled/)).toBeVisible()
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
    expect(screen.getByText(/Tracking policy is unavailable/)).toBeVisible()
  })

  it('makes profile enablement read-only for editors', () => {
    render(<ProfilesSettings enabled={false} onChange={() => undefined} role="editor" />)

    expect(screen.getByRole('switch', { name: 'Enable profiles' })).toBeDisabled()
    expect(screen.getByText(/only owners and admins can change this setting/i)).toBeVisible()
  })
})
