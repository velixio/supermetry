import '@testing-library/jest-dom/vitest'
import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from '@tanstack/react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { Me, Membership, Site } from '@/lib/me'
import { parseSearch, stringifySearch } from '@/router'

import { AccountSidebar } from './AccountSidebar'
import { paletteCommands, type PaletteCommand } from './paletteCommands'
import { SupermetrySidebar } from './Sidebar'
import { SidebarProvider } from './ui/sidebar'
import { TooltipProvider } from './ui/tooltip'

afterEach(cleanup)

const policy: Site['tracking_policy'] = { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] }
const restricted: Site['tracking_policy'] = { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: true, restrictions: ['instance_anonymous'] }
const docs: Site = { id: 7, name: 'Docs', slug: 'docs', public_id: 'docs-public', tracking_policy: policy }
const shop: Site = { id: 8, name: 'Shop', public_id: 'shop-public', tracking_policy: policy }
const acme: Membership = { org: { id: 1, name: 'Acme', slug: 'acme' }, role: 'owner', sites: [docs, shop], effective_capabilities: ['profiles.read', 'replay.read'] }
const other: Membership = { org: { id: 2, name: 'Other', slug: 'other' }, role: 'viewer', sites: [] }
const meFor = (membership: Membership, admin = false): Me => ({
  user: { id: 1, name: 'Owner', email: 'owner@example.test', instance_admin: admin },
  memberships: [membership, other],
})

beforeEach(() => {
  window.scrollTo = () => undefined
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  })
})

// The app's own search serialisation, so the rendered hrefs equal what the running app generates.
function renderWithRouter(children: ReactNode) {
  const rootRoute = createRootRoute({ component: () => children })
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ['/'] }), parseSearch, stringifySearch })
  return render(<RouterProvider router={router} />)
}

const hrefs = (root: HTMLElement) =>
  Object.fromEntries(Array.from(root.querySelectorAll('a')).map((link) => [link.textContent?.trim(), link.getAttribute('href')]))

async function sidebarHrefs(membership: Membership, requestedSite?: string) {
  renderWithRouter(
    <TooltipProvider>
      <SidebarProvider>
        <SupermetrySidebar me={meFor(membership)} membership={membership} requestedSite={requestedSite} />
      </SidebarProvider>
    </TooltipProvider>,
  )
  await screen.findByRole('navigation', { name: 'Settings' })
  const links: Record<string, string | null> = {}
  for (const name of ['Analyze', 'Behaviour', 'Conversion', 'Manage', 'Settings']) {
    const nav = screen.queryByRole('navigation', { name })
    if (nav) Object.assign(links, hrefs(nav))
  }
  return links
}

const pageHrefs = (commands: PaletteCommand[]) =>
  Object.fromEntries(commands.filter((c) => c.group === 'pages' && c.kind === 'navigate').map((c) => [c.label, c.kind === 'navigate' ? c.href : '']))

describe('palette hrefs equal the rendered sidebar links', () => {
  it.each([
    ['owner on a slug site', acme, 'docs'],
    ['owner via the public id alias', acme, 'docs-public'],
    ['owner on a site without slug', acme, 'shop-public'],
    ['owner with an unknown requested site', acme, 'nope'],
    ['viewer', { ...acme, role: 'viewer', effective_capabilities: [] } as Membership, 'docs'],
    ['anonymous-tracking sites', { ...acme, sites: acme.sites.map((site) => ({ ...site, tracking_policy: restricted })) } as Membership, 'docs'],
  ])('%s', async (_name, membership, requested) => {
    const rendered = await sidebarHrefs(membership, requested)
    const commands = paletteCommands({ me: meFor(membership), membership, requestedSite: requested, theme: 'light' })
    const palette = pageHrefs(commands)
    const account = palette['Account settings']
    delete palette['Account settings']
    expect(account).toBe('/app/account/profile')
    expect(palette).toEqual(rendered)
    expect(Object.keys(palette)).toContain('Dashboard')
  })

  it('links the other sites and organizations exactly like the switcher menus', async () => {
    await sidebarHrefs(acme, 'docs')
    fireEvent.pointerDown(screen.getByRole('button', { name: /Docs\s*Acme/ }), { button: 0, ctrlKey: false, pointerType: 'mouse' })
    const menu = await screen.findByRole('menu')
    const items = within(menu).getAllByRole('menuitem')
    const hrefFor = (name: string) => items.find((item) => item.textContent?.trim().endsWith(name))?.getAttribute('href')
    const commands = paletteCommands({ me: meFor(acme), membership: acme, requestedSite: 'docs', theme: 'light' })
    const href = (group: string, label: string) => {
      const command = commands.find((c) => c.group === group && c.label === label)
      return command?.kind === 'navigate' ? command.href : undefined
    }
    expect(hrefFor('Shop')).toMatch(/^\/app\/acme\/shop-public\?period=/)
    expect(href('sites', 'Shop')).toBe(hrefFor('Shop'))
    expect(href('organizations', 'Other')).toBe(hrefFor('Other'))
  })

  it('links account and admin entries like the account sidebar', async () => {
    const me = meFor(acme, true)
    renderWithRouter(
      <TooltipProvider>
        <SidebarProvider>
          <AccountSidebar me={me} />
        </SidebarProvider>
      </TooltipProvider>,
    )
    const nav = await screen.findByRole('navigation', { name: 'Account' })
    const rendered = hrefs(nav)
    const palette = pageHrefs(paletteCommands({ me, theme: 'light' }))
    await waitFor(() => expect(palette).toEqual({ 'Account settings': rendered['Account settings'], 'Instance administration': rendered['Instance administration'] }))
  })
})
