import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { useProfiles } = vi.hoisted(() => ({ useProfiles: vi.fn() }))
vi.mock('./api', () => ({ useProfiles }))

import { ProfilesPage } from './ProfilesPage'
import { renderAuthorizedWithRouter as renderWithRouter } from '@/test/authorization'

afterEach(cleanup)

describe('ProfilesPage', () => {
  it('explains opt-in identity and page-load identification when profiles are disabled', () => {
    useProfiles.mockReturnValue({
      data: { profiles_enabled: false, data: [], next_cursor: null },
      isPending: false,
      isError: false,
    })

    render(<ProfilesPage orgId={1} orgSlug="acme" page={1} search="" siteId={7} siteSlug="docs" onFirst={() => undefined} onNext={() => undefined} onPrevious={() => undefined} onSearchChange={() => undefined} />)

    expect(screen.queryByRole('main')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Profiles are disabled' })).toBeVisible()
    expect(screen.getByText(/enable profiles in general site settings/i)).toBeVisible()
    expect(screen.getByText(/supermetry\.identify\("user-123"/)).toBeVisible()
    expect(screen.getByText(/call identify on every page load/i)).toBeVisible()
  })

  it('searches profiles and renders cursor pagination', async () => {
    const onNext = vi.fn()
    const onSearchChange = vi.fn()
    useProfiles.mockImplementation((_siteId: number, _orgId: number, search: string, cursor?: string) => ({
      data: {
        profiles_enabled: true,
        data: search === 'ada' ? [{
          id: 12,
          site_id: 7,
          external_id: 'ada-42',
          traits: { plan: 'pro' },
          first_seen_at: '2026-10-01T10:00:00Z',
          last_seen_at: '2026-10-08T10:00:00Z',
          sessions: 4,
          events: 19,
        }] : [],
        next_cursor: cursor ? null : 'next-1',
        total: 26,
        total_capped: false,
      },
      isPending: false,
      isError: false,
      isFetching: false,
    }))

    renderWithRouter(<ProfilesPage orgId={1} orgSlug="acme" page={1} search="ada" siteId={7} siteSlug="docs" onFirst={() => undefined} onNext={onNext} onPrevious={() => undefined} onSearchChange={onSearchChange} />)
    fireEvent.change(await screen.findByRole('searchbox', { name: 'Search profiles' }), { target: { value: 'grace' } })
    expect(onSearchChange).toHaveBeenCalledWith('grace')

    expect(await screen.findByRole('link', { name: 'ada-42' })).toHaveAttribute('href', '/app/acme/docs/profiles/12')
    expect(screen.getByRole('cell', { name: '19' })).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    expect(onNext).toHaveBeenCalledWith('next-1')
  })
})
