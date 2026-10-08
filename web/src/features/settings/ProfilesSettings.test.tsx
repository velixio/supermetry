import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ProfilesSettings } from './ProfilesSettings'

const warning = 'Identifies individual users. Only enable this if your users have consented to analytics that can identify them.'

afterEach(cleanup)

describe('ProfilesSettings', () => {
  it('shows the exact consent warning and lets owners enable profiles', () => {
    const onChange = vi.fn()
    render(<ProfilesSettings enabled={false} onChange={onChange} role="owner" />)

    expect(screen.getByText(warning)).toBeVisible()
    fireEvent.click(screen.getByRole('switch', { name: 'Enable profiles' }))
    expect(onChange).toHaveBeenCalledWith(true)
  })

  it('makes profile enablement read-only for editors', () => {
    render(<ProfilesSettings enabled={false} onChange={() => undefined} role="editor" />)

    expect(screen.getByRole('switch', { name: 'Enable profiles' })).toBeDisabled()
    expect(screen.getByText(/only owners and admins can change this setting/i)).toBeVisible()
  })
})
