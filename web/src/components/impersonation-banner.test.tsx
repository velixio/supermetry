import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { ImpersonationBanner } from './ImpersonationBanner'
import { requestLogout } from '@/lib/session-lifecycle'
import type { Me } from '@/lib/me'

vi.mock('@/lib/session-lifecycle', () => ({ requestLogout: vi.fn().mockResolvedValue(undefined) }))

const me: Me = {
  user: { id: 7, name: 'Target', email: 'target@example.test' },
  memberships: [],
}

afterEach(() => { cleanup(); vi.clearAllMocks() })

it('impersonation banner shows the target email and ends the session', () => {
  render(<ImpersonationBanner me={{ ...me, impersonated_by_email: 'root@example.test' }} />)
  const banner = screen.getByRole('alert')
  expect(banner).toHaveTextContent('Signed in as target@example.test by an administrator')
  fireEvent.click(screen.getByRole('button', { name: 'End session' }))
  expect(requestLogout).toHaveBeenCalledTimes(1)
})

it.each([undefined, null])('impersonation banner stays hidden for the user’s own session (%s)', (value) => {
  render(<ImpersonationBanner me={{ ...me, impersonated_by_email: value }} />)
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'End session' })).not.toBeInTheDocument()
})
