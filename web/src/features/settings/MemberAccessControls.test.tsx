import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemberAccessControls, type MemberAccess } from './MemberAccessControls'

const sites = [
  { id: 7, name: 'Docs' },
  { id: 8, name: 'Store' },
]
const viewer: MemberAccess = {
  role: 'viewer',
  site_ids: null,
  capabilities: [],
}
afterEach(cleanup)

describe('member access controls', () => {
  it('makes no sites explicit and distinct from all current and future sites', () => {
    const onChange = vi.fn()
    const view = render(
      <MemberAccessControls
        value={viewer}
        actorRole="owner"
        sites={sites}
        onChange={onChange}
      />,
    )
    fireEvent.change(screen.getByLabelText('Site access'), {
      target: { value: 'none' },
    })
    expect(onChange).toHaveBeenLastCalledWith({ site_ids: [] })
    view.rerender(
      <MemberAccessControls
        value={{ ...viewer, site_ids: [] }}
        actorRole="owner"
        sites={sites}
        onChange={onChange}
      />,
    )
    expect(screen.getByText(/No project data access/)).toBeVisible()
    fireEvent.change(screen.getByLabelText('Site access'), {
      target: { value: 'all' },
    })
    expect(onChange).toHaveBeenLastCalledWith({ site_ids: null })
  })

  it('changes the role without accidentally clearing site restrictions or grants', () => {
    const onChange = vi.fn()
    render(
      <MemberAccessControls
        value={{ ...viewer, site_ids: [], capabilities: ['profiles.read'] }}
        actorRole="owner"
        sites={sites}
        onChange={onChange}
      />,
    )
    fireEvent.change(screen.getByRole('combobox', { name: 'Member role' }), {
      target: { value: 'editor' },
    })
    expect(onChange).toHaveBeenCalledWith({ role: 'editor' })
  })

  it('clears the final sensitive grant with an explicit empty list', () => {
    const onChange = vi.fn()
    render(
      <MemberAccessControls
        value={{ ...viewer, capabilities: ['profiles.read'] }}
        actorRole="owner"
        sites={sites}
        onChange={onChange}
      />,
    )
    fireEvent.click(screen.getByRole('checkbox', { name: /Read profiles/ }))
    expect(onChange).toHaveBeenCalledWith({ capabilities: [] })
  })

  it('shows role-included grants as checked and disabled for admins', () => {
    render(
      <MemberAccessControls
        value={{ ...viewer, role: 'admin' }}
        actorRole="owner"
        sites={sites}
        onChange={vi.fn()}
      />,
    )
    for (const name of [/Read profiles/, /Watch replays/, /Export data/]) {
      expect(screen.getByRole('checkbox', { name })).toBeChecked()
      expect(screen.getByRole('checkbox', { name })).toBeDisabled()
    }
    expect(screen.queryByText(/billing/i)).not.toBeInTheDocument()
  })

  it.each(['owner', 'admin'] as const)('never exposes hosted billing controls to %s', (actorRole) => {
    render(
      <MemberAccessControls
        value={{ ...viewer, site_ids: [] }}
        actorRole={actorRole}
        sites={sites}
        onChange={vi.fn()}
      />,
    )
    expect(screen.queryByText(/billing/i)).not.toBeInTheDocument()
    expect(screen.getByLabelText('Site access')).toHaveValue('none')
    for (const name of [/Read profiles/, /Watch replays/, /Export data/])
      expect(screen.getByRole('checkbox', { name })).not.toBeChecked()
  })
})
