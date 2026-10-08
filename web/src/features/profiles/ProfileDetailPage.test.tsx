import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
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
        orgId={1}
        profileId={12}
        role={role}
      />
    </QueryClientProvider>,
  )
}

describe('ProfileDetailPage', () => {
  it('shows traits and pages through the hot-tier event timeline', () => {
    useDeleteProfile.mockReturnValue({ mutate: vi.fn(), isPending: false })
    useProfile.mockImplementation((_id: number, _orgId: number, cursor?: string) => ({
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
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
    expect(useProfile).toHaveBeenLastCalledWith(12, 1, 'events-2')
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
