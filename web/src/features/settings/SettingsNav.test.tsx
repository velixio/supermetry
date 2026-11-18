import '@testing-library/jest-dom/vitest'

import { cleanup, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { renderWithRouter } from '@/test/renderWithRouter'
import { SettingsNav, settingsGroups } from './SettingsNav'

afterEach(cleanup)

describe('settings sub-navigation', () => {
  it('groups pages and marks the current one with aria-current', async () => {
    renderWithRouter(<SettingsNav org="acme" current="mcp" showReplay importsSite="main" />)
    const nav = await screen.findByRole('navigation', { name: 'Organization settings' })
    for (const group of ['General', 'People', 'Developers', 'Security', 'Data']) {
      expect(within(nav).getByRole('group', { name: group })).toBeInTheDocument()
    }
    expect(within(nav).getByRole('link', { name: 'MCP' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'API keys' })).not.toHaveAttribute('aria-current')
    expect(within(nav).queryByRole('tab')).not.toBeInTheDocument()
  })

  it('keeps every old URL as a link target and adds the new pages', async () => {
    renderWithRouter(<SettingsNav org="acme" current="settings" showReplay importsSite="main" />)
    const nav = await screen.findByRole('navigation', { name: 'Organization settings' })
    const hrefs = (label: string) => within(nav).getByRole('link', { name: label }).getAttribute('href')
    expect(hrefs('Overview')).toBe('/app/acme/settings')
    expect(hrefs('Members')).toBe('/app/acme/settings/members')
    expect(hrefs('Invitations')).toBe('/app/acme/settings/invites')
    expect(hrefs('API keys')).toBe('/app/acme/settings/api-keys')
    expect(hrefs('API reference')).toBe('/app/acme/settings/api')
    expect(hrefs('Audit log')).toBe('/app/acme/settings/audit')
    expect(hrefs('Notifications')).toBe('/app/acme/settings/notifications')
    expect(hrefs('Replay storage')).toBe('/app/acme/settings/replay-storage')
    expect(hrefs('Exports')).toBe('/app/acme/settings/exports')
    expect(hrefs('MCP')).toBe('/app/acme/settings/mcp')
    expect(hrefs('Sessions')).toBe('/app/account/sessions')
    expect(hrefs('Imports')).toBe('/app/acme/main/settings/imports')
  })

  it('shows only Overview to people who cannot manage the organization', () => {
    const groups = settingsGroups({ showReplay: false, canManage: false }, 'acme')
    expect(groups.flatMap((g) => g.items.map((i) => i.label))).toEqual(['Overview'])
  })

  it('omits Replay storage and Imports when unavailable', () => {
    const labels = settingsGroups({ showReplay: false, canManage: true }, 'acme').flatMap((g) => g.items.map((i) => i.label))
    expect(labels).not.toContain('Replay storage')
    expect(labels).not.toContain('Imports')
  })

  it('offers a labelled select for small screens', async () => {
    renderWithRouter(<SettingsNav org="acme" current="members" />)
    const select = await screen.findByLabelText('Settings section')
    expect(select).toHaveValue('/app/acme/settings/members')
  })
})
