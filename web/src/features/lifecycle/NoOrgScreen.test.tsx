import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { getDeletedOrganizations } from './api'
import { NoOrgScreen } from './NoOrgScreen'
import { renderWithRouter } from '@/test/renderWithRouter'

vi.mock('./api', () => ({ getDeletedOrganizations: vi.fn(), restoreOrganization: vi.fn() }))

afterEach(cleanup)

it('offers organization creation and the deleted organizations list', async () => {
  vi.mocked(getDeletedOrganizations).mockResolvedValue([])
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

  renderWithRouter(
    <QueryClientProvider client={queryClient}>
      <NoOrgScreen navigate={vi.fn()} />
    </QueryClientProvider>,
  )

  expect(await screen.findByRole('heading', { name: "You don't belong to an organization" })).toBeVisible()
  expect(screen.getByRole('link', { name: 'Create organization' })).toHaveAttribute('href', '/app/account/organizations/new')
  expect(await screen.findByText('No organizations are scheduled for deletion.')).toBeVisible()
})
