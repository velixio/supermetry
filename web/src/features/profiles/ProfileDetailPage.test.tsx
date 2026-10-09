import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { useDeleteProfile, useProfile } = vi.hoisted(() => ({
  useDeleteProfile: vi.fn(),
  useProfile: vi.fn(),
}))
vi.mock('./api', () => ({ useDeleteProfile, useProfile }))

import { ProfileDetailPage } from './ProfileDetailPage'

afterEach(cleanup)

function renderPage(role: 'owner' | 'admin' | 'editor' | 'viewer' = 'owner') {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <ProfileDetailPage
        onDeleted={() => undefined}
        onFirst={() => undefined}
        onNext={() => undefined}
        onPrevious={() => undefined}
        orgId={1}
        page={1}
        profileId={12}
        siteId={7}
        role={role}
      />
    </QueryClientProvider>,
  )
}

describe('ProfileDetailPage', () => {
  it('does not render a cached profile from a different site', () => {
    useDeleteProfile.mockReturnValue({ mutate: vi.fn(), isPending: false })
    useProfile.mockReturnValue({ data: { id: 12, site_id: 99, external_id: 'FOREIGN_PROFILE', traits: { email: 'private@example.test' }, timeline: [] }, isPending: false, isError: false })
    renderPage()
    expect(screen.getByRole('alert')).toHaveTextContent('selected site')
    expect(screen.queryByText('FOREIGN_PROFILE')).not.toBeInTheDocument()
    expect(screen.queryByText('private@example.test')).not.toBeInTheDocument()
  })

  it('shows traits and pages through the hot-tier event timeline', () => {
    useDeleteProfile.mockReturnValue({ mutate: vi.fn(), isPending: false })
    useProfile.mockImplementation((_id: number, _orgId: number, _siteId: number, cursor?: string) => ({
      data: {
        id: 12,
        site_id: 7,
        external_id: 'ada-42',
        traits: { plan: 'pro', region: 'eu' },
        first_seen_at: '2026-10-01T10:00:00Z',
        last_seen_at: '2026-10-08T10:00:00Z',
        timeline: [{ ts: '2026-10-08T10:00:00Z', kind: 'event', name: 'Signup', path: '/pricing', props: { source: 'nav' } }],
        next_cursor: cursor ? null : 'events-2',
      },
      isPending: false,
      isError: false,
      isFetching: false,
    }))

    renderPage()

    expect(screen.getByRole('heading', { name: 'ada-42' })).toBeVisible()
    expect(screen.getByText('plan')).toBeVisible()
    expect(screen.getByText('pro')).toBeVisible()
    expect(screen.getByText('Signup')).toBeVisible()
    expect(screen.getByText('/pricing')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Next' })).toBeEnabled()
  })

  it('requires an accessible confirmation before an owner deletes a profile', async () => {
    const mutate = vi.fn()
    useDeleteProfile.mockReturnValue({ mutate, isPending: false, error: null })
    useProfile.mockReturnValue({
      data: { id: 12, site_id: 7, external_id: 'ada-42', traits: {}, first_seen_at: '', last_seen_at: '', timeline: [], next_cursor: null },
      isPending: false,
      isError: false,
      isFetching: false,
    })

    renderPage('owner')
    fireEvent.click(screen.getByRole('button', { name: 'Delete profile' }))

    expect(await screen.findByRole('alertdialog')).toHaveAccessibleName('Delete ada-42?')
    expect(screen.getByText(/permanently erases this profile and its events/i)).toBeVisible()
    expect(screen.getByText(/historical aggregate rollups cannot be un-counted/i)).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Delete profile permanently' }))
    expect(mutate).toHaveBeenCalledWith(12)
  })

  it('does not offer profile deletion to editors', () => {
    useDeleteProfile.mockReturnValue({ mutate: vi.fn(), isPending: false })
    useProfile.mockReturnValue({
      data: { id: 12, site_id: 7, external_id: 'ada-42', traits: {}, first_seen_at: '', last_seen_at: '', timeline: [], next_cursor: null },
      isPending: false,
      isError: false,
      isFetching: false,
    })

    renderPage('editor')
    expect(screen.queryByRole('button', { name: 'Delete profile' })).not.toBeInTheDocument()
  })
})
