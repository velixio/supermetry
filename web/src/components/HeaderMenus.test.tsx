import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { AnchorHTMLAttributes } from 'react'

import { AccountMenu } from './HeaderMenus'
import { requestLogout } from '@/lib/session-lifecycle'
import type { Me } from '@/lib/me'

vi.mock('@/lib/session-lifecycle', () => ({ requestLogout: vi.fn().mockResolvedValue(undefined) }))
vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...await importOriginal<object>(),
  Link: ({ to, params, ...props }: AnchorHTMLAttributes<HTMLAnchorElement> & { to: string; params?: Record<string, string> }) => (
    <a {...props} href={to.replace(/\$(\w+)/g, (_, key: string) => params?.[key] ?? key)} />
  ),
}))

const me: Me = {
  user: { id: 1, name: 'Alex Example', email: 'alex@example.test' },
  memberships: [{ org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [] }],
}
function openMenu(name: string) {
  fireEvent.keyDown(screen.getByRole('button', { name }), { key: 'ArrowDown' })
}
afterEach(() => { cleanup(); vi.clearAllMocks() })

it.each([true, false])('shows identity and explicit current-session logout with organization access=%s', async (hasOrganization) => {
  render(<AccountMenu me={{ ...me, memberships: hasOrganization ? me.memberships : [] }} />)
  openMenu('Account')
  expect(await screen.findByText('Alex Example')).toBeVisible()
  expect(screen.getByText('alex@example.test')).toBeVisible()
  expect(screen.getByText('Supermetry dev')).toBeVisible()
  expect(screen.getByRole('menuitem', { name: 'Account settings' })).toHaveAttribute('href', '/app/account/profile')
  expect(screen.queryByRole('menuitem', { name: 'Instance administration' })).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('menuitem', { name: 'Log out' }))
  expect(requestLogout).toHaveBeenCalledTimes(1)
})

it('shows the server build version in the account menu', async () => {
  render(<AccountMenu me={{ ...me, version: '1.4.2' }} />)
  openMenu('Account')
  expect(await screen.findByText('Supermetry 1.4.2')).toBeVisible()
})

it('opens the same account menu from a caller-supplied trigger', async () => {
  render(
    <AccountMenu
      me={{ ...me, user: { ...me.user, instance_admin: true } }}
      side="right"
      trigger={<button type="button">Account</button>}
    />,
  )
  expect(screen.getAllByRole('button', { name: 'Account' })).toHaveLength(1)
  openMenu('Account')
  expect(await screen.findByRole('menu', { name: 'Account menu' })).toBeVisible()
  expect(screen.getByText('Alex Example')).toBeVisible()
  expect(screen.getByRole('menuitem', { name: 'Account settings' })).toHaveAttribute('href', '/app/account/profile')
  expect(screen.getByRole('menuitem', { name: 'Instance administration' })).toHaveAttribute('href', '/app/admin/users')
  fireEvent.click(screen.getByRole('menuitem', { name: 'Log out' }))
  expect(requestLogout).toHaveBeenCalledTimes(1)
})
