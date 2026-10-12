import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { AnchorHTMLAttributes } from 'react'

import { AccountMenu, HeaderMenus } from './HeaderMenus'
import { AccountFrame } from './AccountFrame'
import { requestLogout } from '@/lib/session-lifecycle'
import type { Me, Membership, Site } from '@/lib/me'

vi.mock('@/lib/session-lifecycle', () => ({ requestLogout: vi.fn().mockResolvedValue(undefined) }))
vi.mock('@/lib/theme', () => ({ useTheme: () => ({ theme: 'light', toggle: vi.fn() }) }))
vi.mock('./ui/sonner', () => ({ Toaster: () => null }))
vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...await importOriginal<object>(),
  Link: ({ to, params, ...props }: AnchorHTMLAttributes<HTMLAnchorElement> & { to: string; params?: Record<string, string> }) => (
    <a {...props} href={to.replace(/\$(\w+)/g, (_, key: string) => params?.[key] ?? key)} />
  ),
}))

const site: Site = { id: 7, name: 'Docs', slug: 'docs', public_id: 'public-docs' }
const membership: Membership = {
  org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [site],
}
const me: Me = {
  user: { id: 1, name: 'Alex Example', email: 'alex@example.test' }, memberships: [membership],
}
function openMenu(name: string) {
  fireEvent.keyDown(screen.getByRole('button', { name }), { key: 'ArrowDown' })
}
afterEach(() => { cleanup(); vi.clearAllMocks() })

it.each(['viewer', 'editor', 'admin', 'owner'] as const)('scopes settings links for %s', async (role) => {
  render(<HeaderMenus me={me} membership={{ ...membership, role }} site={site} />)
  openMenu('Settings')
  expect(await screen.findByRole('menuitem', { name: /Organization settings\s*Acme/ })).toHaveAttribute('href', '/app/acme/settings')
  if (role === 'viewer') {
    expect(screen.queryByRole('menuitem', { name: /Site settings\s*Docs/ })).not.toBeInTheDocument()
  } else {
    expect(screen.getByRole('menuitem', { name: /Site settings\s*Docs/ })).toHaveAttribute('href', '/app/acme/docs/settings/general')
  }
})

it('offers only organization settings when no site is selected', async () => {
  render(<HeaderMenus me={me} membership={{ ...membership, sites: [] }} />)
  openMenu('Settings')
  expect(await screen.findByRole('menuitem', { name: /Organization settings\s*Acme/ })).toBeVisible()
  expect(screen.queryByRole('menuitem', { name: /Site settings/ })).not.toBeInTheDocument()
})

it('lists each organization from account pages without inventing a current site', async () => {
  const second = { ...membership, org: { id: 2, name: 'Other', slug: 'other' } }
  render(<HeaderMenus me={{ ...me, memberships: [membership, second] }} />)
  openMenu('Settings')
  expect(await screen.findByRole('menuitem', { name: /Organization settings\s*Acme/ })).toHaveAttribute('href', '/app/acme/settings')
  expect(screen.getByRole('menuitem', { name: /Organization settings\s*Other/ })).toHaveAttribute('href', '/app/other/settings')
  expect(screen.queryByRole('menuitem', { name: /Site settings/ })).not.toBeInTheDocument()
})

it('uses the selected organization and site rather than the first membership', async () => {
  const secondSite = { id: 9, name: 'Store', public_id: 'store-public' }
  const second = { ...membership, org: { id: 2, name: 'Other', slug: 'other' }, sites: [secondSite] }
  render(<HeaderMenus me={{ ...me, memberships: [membership, second] }} membership={second} site={secondSite} />)
  openMenu('Settings')
  expect(await screen.findByRole('menuitem', { name: /Organization settings\s*Other/ })).toHaveAttribute('href', '/app/other/settings')
  expect(screen.getByRole('menuitem', { name: /Site settings\s*Store/ })).toHaveAttribute('href', '/app/other/store-public/settings/general')
  expect(screen.queryByRole('menuitem', { name: /Organization settings\s*Acme/ })).not.toBeInTheDocument()
})

it.each([true, false])('shows identity and explicit current-session logout with organization access=%s', async (hasOrganization) => {
  render(<HeaderMenus me={{ ...me, memberships: hasOrganization ? me.memberships : [] }} />)
  if (!hasOrganization) expect(screen.queryByRole('button', { name: 'Settings' })).not.toBeInTheDocument()
  openMenu('Account')
  expect(await screen.findByText('Alex Example')).toBeVisible()
  expect(screen.getByText('alex@example.test')).toBeVisible()
  expect(screen.getByText('Supermetry dev')).toBeVisible()
  expect(screen.getByRole('menuitem', { name: 'Account settings' })).toHaveAttribute('href', '/app/account/profile')
  fireEvent.click(screen.getByRole('menuitem', { name: 'Log out' }))
  expect(requestLogout).toHaveBeenCalledTimes(1)
})

it('shows the server build version in the account menu', async () => {
  render(<HeaderMenus me={{ ...me, version: '1.4.2' }} />)

  openMenu('Account')
  expect(await screen.findByText('Supermetry 1.4.2')).toBeVisible()
})

it('keeps account content and account controls accessible without memberships', async () => {
  render(<AccountFrame me={{ ...me, memberships: [] }}><h1>Profile</h1></AccountFrame>)
  expect(screen.getByRole('heading', { name: 'Profile' })).toBeVisible()
  expect(screen.getByRole('link', { name: 'Supermetry' })).toHaveAttribute('href', '/app')
  expect(screen.getByRole('button', { name: 'Use dark theme' })).toBeVisible()
  expect(screen.queryByRole('button', { name: 'Settings' })).not.toBeInTheDocument()
  openMenu('Account')
  expect(await screen.findByRole('menuitem', { name: 'Log out' })).toBeVisible()
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
