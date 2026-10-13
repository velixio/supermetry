import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { useUpdateStatus } = vi.hoisted(() => ({ useUpdateStatus: vi.fn() }))
vi.mock('./adminApi', () => ({ useUpdateStatus }))

import { AdminUpdatePage } from './AdminUpdatePage'

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('AdminUpdatePage', () => {
  it('shows the installed and available versions with the upstream release link', () => {
    useUpdateStatus.mockReturnValue({
      data: {
        current_version: '1.2.0', latest_version: '1.3.0', update_available: true,
        release_url: 'https://github.com/velixio/supermetry/releases/tag/v1.3.0',
        published_at: '2026-10-14T08:00:00Z', checked_at: '2026-10-14T09:00:00Z', check_error: null,
      },
      isPending: false, error: null,
    })

    render(<AdminUpdatePage />)

    expect(screen.getByRole('heading', { level: 1, name: 'System update' })).toBeVisible()
    expect(screen.getByText('1.2.0')).toBeVisible()
    expect(screen.getByText('1.3.0')).toBeVisible()
    expect(screen.getByRole('link', { name: 'View release notes' })).toHaveAttribute(
      'href', 'https://github.com/velixio/supermetry/releases/tag/v1.3.0',
    )
    expect(screen.getByText('Installing updates from this page is not enabled yet.')).toBeVisible()
    expect(screen.queryByRole('button', { name: /update/i })).not.toBeInTheDocument()
  })

  it('shows an up-to-date state without a release link', () => {
    useUpdateStatus.mockReturnValue({
      data: {
        current_version: '1.3.0', latest_version: '1.3.0', update_available: false,
        release_url: null, published_at: null, checked_at: '2026-10-14T09:00:00Z', check_error: null,
      },
      isPending: false, error: null,
    })
    render(<AdminUpdatePage />)
    expect(screen.getByText('Supermetry is up to date.')).toBeVisible()
    expect(screen.queryByRole('link', { name: 'View release notes' })).not.toBeInTheDocument()
  })

  it('shows release-check and request errors honestly', () => {
    useUpdateStatus.mockReturnValue({
      data: {
        current_version: '1.2.0', latest_version: null, update_available: false,
        release_url: null, published_at: null, checked_at: '2026-10-14T09:00:00Z', check_error: 'GitHub could not be reached.',
      },
      isPending: false, error: new Error('Status endpoint unavailable.'),
    })
    render(<AdminUpdatePage />)
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Status endpoint unavailable.')
    expect(alert).toHaveTextContent('GitHub could not be reached.')
    expect(screen.queryByText('Supermetry is up to date.')).not.toBeInTheDocument()
  })
})
