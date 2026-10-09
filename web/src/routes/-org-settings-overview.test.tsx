import '@testing-library/jest-dom/vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { ComponentType, ReactNode } from 'react'
import { Route } from './$org.settings.index'
import { useOrganization } from '@/features/settings/routeContext'

vi.mock('@/features/settings/routeContext', () => ({ useOrganization: vi.fn() }))
vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: { component: ComponentType }) => ({
    options,
    useParams: () => ({ org: 'acme' }),
  }),
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))
afterEach(cleanup)

it.each(['owner', 'admin', 'editor', 'viewer'] as const)('shows %s access without hosted billing cards or badges', (role) => {
  vi.mocked(useOrganization).mockReturnValue({
    org: { id: 1, name: 'Acme', slug: 'acme' }, role, sites: [],
    billing_admin: true, effective_capabilities: ['billing.manage'],
  })
  const Overview = Route.options.component as ComponentType
  render(<Overview />)
  expect(screen.getByRole('heading', { name: 'Acme' })).toBeVisible()
  expect(screen.getByText(/No project data access/)).toBeVisible()
  expect(screen.getByRole('link', { name: 'Account settings' })).toHaveAttribute('href', '/account/profile')
  expect(screen.queryByText(/billing/i)).not.toBeInTheDocument()
})
