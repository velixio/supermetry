import '@testing-library/jest-dom/vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { ImpersonationBanner } from './ImpersonationBanner'
import { requestLogout } from '@/lib/session-lifecycle'
import type { Me } from '@/lib/me'

vi.mock('@/lib/session-lifecycle', () => ({ requestLogout: vi.fn().mockResolvedValue(undefined) }))

const me: Me = {
  user: { id: 7, name: 'Target', email: 'target@example.test' },
  memberships: [],
}

afterEach(() => { cleanup(); vi.clearAllMocks(); vi.useRealTimers() })

it('names the customer, the admin and the end time, and ends with a notice on sign-in', () => {
  vi.useFakeTimers()
  const expires = new Date(2026, 9, 17, 14, 30)
  render(<ImpersonationBanner me={{ ...me, impersonated_by_email: 'root@example.test', impersonation_expires_at: expires.toISOString() }} />)
  const banner = screen.getByRole('alert')
  expect(banner).toHaveTextContent('You are signed in as target@example.test by root@example.test.')
  expect(banner).toHaveTextContent(/The session ends at .*30/)
  expect(banner).toHaveClass('sticky')
  act(() => { vi.advanceTimersByTime(1_500) })
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.getByRole('region', { name: 'Support session' })).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'End session' }))
  expect(requestLogout).toHaveBeenCalledWith('/login?notice=impersonation')
})

it.each([undefined, null])('impersonation banner stays hidden for the user’s own session (%s)', (value) => {
  render(<ImpersonationBanner me={{ ...me, impersonated_by_email: value }} />)
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'End session' })).not.toBeInTheDocument()
})
