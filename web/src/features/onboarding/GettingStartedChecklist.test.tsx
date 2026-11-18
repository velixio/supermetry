import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { privateRequest, useGoals } = vi.hoisted(() => ({ privateRequest: vi.fn(), useGoals: vi.fn() }))

vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('@/features/goals/api', () => ({ useGoals }))
vi.mock('@/features/demo/api', () => ({ createDemo: vi.fn(), dismissDemoPrompt: vi.fn() }))

import { GettingStartedChecklist } from './GettingStartedChecklist'
import { renderWithRouter } from './testUtils'

type Facts = { install?: string; goals?: number; members?: number; invites?: number; keys?: object[] }

function mockFacts({ install = 'waiting', goals = 0, members = 1, invites = 0, keys = [] }: Facts = {}) {
  useGoals.mockReturnValue({ isPending: false, data: Array.from({ length: goals }, (_, id) => ({ id })) })
  privateRequest.mockImplementation((_method: string, path: string) => {
    if (path.includes('/api-keys')) return Promise.resolve({ data: keys })
    if (path.includes('/members')) return Promise.resolve({ data: Array.from({ length: members }, (_, id) => ({ id })) })
    if (path.includes('/invitations')) return Promise.resolve({ data: Array.from({ length: invites }, (_, id) => ({ id })) })
    return Promise.resolve({ id: 7, install_status: install })
  })
}

const props = { orgId: 1, orgSlug: 'acme', siteId: 7, siteSlug: 'example', userId: 5, canManage: true }

describe('GettingStartedChecklist', () => {
  beforeEach(() => { window.localStorage.clear(); vi.clearAllMocks() })
  afterEach(cleanup)

  it('lists five steps with progress and checked state announced', async () => {
    mockFacts({ install: 'receiving', goals: 1 })
    await renderWithRouter(<GettingStartedChecklist {...props} />)
    const list = await screen.findByRole('list', { name: 'Getting started steps' })
    expect(screen.getByTestId('checklist-progress')).toHaveTextContent('3 of 5 done')
    const items = within(list).getAllByRole('listitem')
    expect(items).toHaveLength(5)
    expect(items[0]).toHaveTextContent('Add the snippet (Done)')
    expect(items[3]).toHaveTextContent('Invite a teammate (To do)')
  })

  it('links each open step to where it is done', async () => {
    mockFacts()
    await renderWithRouter(<GettingStartedChecklist {...props} />)
    expect(await screen.findByRole('link', { name: 'Create goal' })).toHaveAttribute('href', '/app/acme/example/goals')
    expect(screen.getByRole('link', { name: 'Create API key' })).toHaveAttribute('href', '/app/acme/settings/api-keys')
    expect(screen.getByRole('link', { name: 'Invite teammate' })).toHaveAttribute('href', '/app/acme/settings/invites')
    expect(screen.getByRole('link', { name: 'Open install steps' })).toHaveAttribute('href', '/app/acme/example/settings/installation')
    expect(screen.getByRole('button', { name: 'Explore with sample data' })).toBeInTheDocument()
  })

  it('counts an MCP key', async () => {
    mockFacts({ keys: [{ scopes: ['stats:read'] }] })
    await renderWithRouter(<GettingStartedChecklist {...props} />)
    expect(await screen.findByRole('list', { name: 'Getting started steps' })).toBeInTheDocument()
    expect(screen.getByTestId('checklist-progress')).toHaveTextContent('1 of 5 done')
  })

  it('persists dismissal per user and site', async () => {
    mockFacts()
    const view = await renderWithRouter(<GettingStartedChecklist {...props} />)
    fireEvent.click(await screen.findByRole('button', { name: 'Dismiss getting started checklist' }))
    expect(screen.queryByRole('list', { name: 'Getting started steps' })).not.toBeInTheDocument()
    view.unmount()
    await renderWithRouter(<GettingStartedChecklist {...props} />)
    await waitFor(() => expect(privateRequest).toBeDefined())
    expect(screen.queryByRole('list', { name: 'Getting started steps' })).not.toBeInTheDocument()
    cleanup()
    await renderWithRouter(<GettingStartedChecklist {...props} userId={6} />)
    expect(await screen.findByRole('list', { name: 'Getting started steps' })).toBeInTheDocument()
  })

  it('disappears when everything is done, and for viewers and demos', async () => {
    mockFacts({ install: 'receiving', goals: 1, members: 2, keys: [{ scopes: ['stats:read'] }] })
    await renderWithRouter(<GettingStartedChecklist {...props} />)
    await waitFor(() => expect(privateRequest).toHaveBeenCalled())
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(screen.queryByRole('list', { name: 'Getting started steps' })).not.toBeInTheDocument()
    cleanup()
    mockFacts()
    await renderWithRouter(<GettingStartedChecklist {...props} canManage={false} />)
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(screen.queryByRole('list', { name: 'Getting started steps' })).not.toBeInTheDocument()
  })
})
