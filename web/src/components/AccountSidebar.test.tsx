import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { SidebarProvider } from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import type { Me } from '@/lib/me'
import { renderWithRouter } from '@/test/renderWithRouter'

import { AccountSidebar } from './AccountSidebar'

const me: Me = {
  user: { id: 1, name: 'Owner', email: 'owner@example.test' },
  memberships: [
    { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [{ id: 7, name: 'Docs', slug: 'docs' }] },
    { org: { id: 2, name: 'Other', slug: 'other' }, role: 'viewer', sites: [] },
  ],
}

beforeEach(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  })
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: 1440 })
})
afterEach(cleanup)

function show(path: string, user: Me = me) {
  renderWithRouter(
    <TooltipProvider>
      <SidebarProvider>
        <AccountSidebar me={user} />
      </SidebarProvider>
    </TooltipProvider>,
    path,
  )
  return screen.findByRole('complementary', { name: 'Application sidebar' })
}

function openSwitcher(sidebar: HTMLElement) {
  fireEvent.pointerDown(within(sidebar).getByRole('button', { name: 'Choose organization' }), { button: 0, ctrlKey: false, pointerType: 'mouse' })
  return screen.findByRole('menu')
}

describe('AccountSidebar', () => {
  it('links account settings, marks it current, and keeps the brand link and account menu', async () => {
    const sidebar = await show('/app/account/security')
    const nav = within(sidebar).getByRole('navigation', { name: 'Account' })
    expect(within(nav).getAllByRole('link').map((link) => link.textContent)).toEqual(['Account settings'])
    const link = within(nav).getByRole('link', { name: 'Account settings' })
    expect(link).toHaveAttribute('href', '/app/account/profile')
    expect(link).toHaveAttribute('aria-current', 'page')
    expect(within(sidebar).getByRole('link', { name: 'Supermetry home' })).toHaveAttribute('href', '/app')
    expect(within(sidebar).getByRole('button', { name: 'Account' })).toBeInTheDocument()
    expect(within(sidebar).queryByRole('navigation', { name: 'Settings' })).toBeNull()
  })

  it('adds instance administration for instance admins only', async () => {
    const sidebar = await show('/app/admin/sites', { ...me, user: { ...me.user, instance_admin: true } })
    const nav = within(sidebar).getByRole('navigation', { name: 'Account' })
    const admin = within(nav).getByRole('link', { name: 'Instance administration' })
    expect(admin).toHaveAttribute('href', '/app/admin/users')
    expect(admin).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Account settings' })).not.toHaveAttribute('aria-current')
  })

  it('links GitHub stars in the sidebar for instance admins only', async () => {
    const admin = await show('/app/github-stars', { ...me, user: { ...me.user, instance_admin: true } })
    const link = within(within(admin).getByRole('navigation', { name: 'Community' })).getByRole('link', { name: 'GitHub stars' })
    expect(link).toHaveAttribute('href', '/app/github-stars')
    expect(link).toHaveAttribute('aria-current', 'page')
    cleanup()
    const member = await show('/app/account/profile')
    expect(within(member).queryByRole('link', { name: 'GitHub stars' })).toBeNull()
    expect(within(member).queryByRole('navigation', { name: 'Community' })).toBeNull()
  })

  it('switches organizations without offering sites', async () => {
    const menu = await openSwitcher(await show('/app/account/profile'))
    expect(within(menu).getByRole('menuitem', { name: 'Acme' }).getAttribute('href')).toMatch(/^\/app\/acme\/docs\?/)
    expect(within(menu).getByRole('menuitem', { name: 'Other' })).toHaveAttribute('href', '/app/other/settings')
    expect(within(menu).getByRole('menuitem', { name: /Add organization/ })).toHaveAttribute('href', '/app/account/organizations/new')
    expect(within(menu).queryByRole('menuitem', { name: /Add site/ })).toBeNull()
    expect(within(menu).queryByText('Projects')).toBeNull()
  })

  it('offers only Add organization to a user without memberships', async () => {
    const menu = await openSwitcher(await show('/app/account/profile', { ...me, memberships: [] }))
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['+ Add organization'])
  })
})
