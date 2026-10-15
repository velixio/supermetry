import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useUpdateStatus, useApplyUpdate, useCheckUpdate, useMe, mutate, checkMutate } = vi.hoisted(() => ({
  useUpdateStatus: vi.fn(), useApplyUpdate: vi.fn(), useCheckUpdate: vi.fn(), useMe: vi.fn(), mutate: vi.fn(), checkMutate: vi.fn(),
}))
vi.mock('./adminApi', () => ({ useUpdateStatus, useApplyUpdate, useCheckUpdate }))
vi.mock('@/lib/me', () => ({ useMe }))

import { AdminUpdatePage } from './AdminUpdatePage'

beforeEach(() => {
  useCheckUpdate.mockReturnValue({ mutate: checkMutate, isPending: false, error: null })
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

function available(overrides: Record<string, unknown> = {}) {
  useMe.mockReturnValue({ data: { user: { totp_enabled: true } } })
  useApplyUpdate.mockReturnValue({ mutate, isPending: false, error: null, reset: vi.fn() })
  useUpdateStatus.mockReturnValue({
    data: {
      current_version: '1.2.0', latest_version: '1.3.0', update_available: true,
      release_url: 'https://github.com/velixio/supermetry/releases/tag/v1.3.0',
      published_at: '2026-10-14T08:00:00Z', checked_at: '2026-10-14T09:00:00Z', check_error: null,
      update_supported: true, deployment_mode: 'binary', job: null, ...overrides,
    },
    isPending: false, error: null,
  })
}

describe('AdminUpdatePage', () => {
  it('shows the installed and available versions with the upstream release link', () => {
    available()

    render(<AdminUpdatePage />)

    expect(screen.getByRole('heading', { level: 1, name: 'System update' })).toBeVisible()
    expect(screen.getByText('1.2.0')).toBeVisible()
    expect(screen.getByText('1.3.0')).toBeVisible()
    expect(screen.getByRole('link', { name: 'View release notes' })).toHaveAttribute(
      'href', 'https://github.com/velixio/supermetry/releases/tag/v1.3.0',
    )
    expect(screen.getByText('Binary installation')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Update to 1.3.0' })).toBeVisible()
  })

  it('checks for a new release on demand, showing progress while it runs', () => {
    available({ update_available: false, latest_version: '1.2.0' })
    const { rerender } = render(<AdminUpdatePage />)

    fireEvent.click(screen.getByRole('button', { name: 'Check for updates' }))
    expect(checkMutate).toHaveBeenCalledOnce()

    useCheckUpdate.mockReturnValue({ mutate: checkMutate, isPending: true, error: null })
    rerender(<AdminUpdatePage />)
    expect(screen.getByRole('button', { name: 'Checking…' })).toBeDisabled()
  })

  it('requires the password and TOTP before dispatching once', async () => {
    available()
    render(<AdminUpdatePage />)

    fireEvent.click(screen.getByRole('button', { name: 'Update to 1.3.0' }))
    expect(screen.getByRole('dialog', { name: 'Update Supermetry to 1.3.0?' })).toBeVisible()
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'pw-123456789' } })
    fireEvent.change(screen.getByLabelText('Authentication code'), { target: { value: '123456' } })
    expect(screen.getByRole('button', { name: 'Start update' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: 'Start update' }))

    await waitFor(() => expect(mutate).toHaveBeenCalledOnce())
    expect(mutate).toHaveBeenCalledWith(expect.objectContaining({
      target_version: '1.3.0', password: 'pw-123456789', totp_code: '123456',
      idempotency_key: expect.any(String),
    }), expect.any(Object))
  })

  it('does not request a TOTP code when two-factor authentication is off', async () => {
    available()
    useMe.mockReturnValue({ data: { user: { totp_enabled: false } } })
    render(<AdminUpdatePage />)

    fireEvent.click(screen.getByRole('button', { name: 'Update to 1.3.0' }))
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'pw-123456789' } })
    expect(screen.queryByLabelText('Authentication code')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Start update' }))

    await waitFor(() => expect(mutate).toHaveBeenCalledOnce())
    expect(mutate.mock.calls[0][0]).not.toHaveProperty('totp_code')
  })

  it('shows active update progress and prevents a second update', () => {
    available({
      job: {
        id: 4, from_version: 'v1.2.0', target_version: 'v1.3.0', deployment_mode: 'binary',
        status: 'backing_up', created_at: '2026-10-14T09:01:00Z', updated_at: '2026-10-14T09:02:00Z',
      },
    })
    render(<AdminUpdatePage />)
    expect(screen.getByRole('status', { name: 'Update progress' })).toHaveTextContent('Backing up the database')
    expect(screen.queryByRole('button', { name: 'Update to 1.3.0' })).not.toBeInTheDocument()
  })

  it('shows recovery-required instructions prominently', () => {
    available({
      update_available: false,
      job: {
        id: 5, from_version: 'v1.2.0', target_version: 'v1.3.0', deployment_mode: 'docker',
        status: 'recovery_required', created_at: '2026-10-14T09:01:00Z', updated_at: '2026-10-14T09:02:00Z', finished_at: '2026-10-14T09:02:00Z',
      },
    })
    render(<AdminUpdatePage />)
    expect(screen.getByRole('alert')).toHaveTextContent('Manual recovery is required')
    expect(screen.getByRole('alert')).toHaveTextContent('Do not start another update')
  })

  it('explains when this deployment cannot be updated from the UI', () => {
    available({ update_supported: false, deployment_mode: undefined })
    render(<AdminUpdatePage />)
    expect(screen.getByText('Updates are not managed by this installation.')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Update to 1.3.0' })).not.toBeInTheDocument()
  })

  it('shows an up-to-date state without a release link', () => {
    useUpdateStatus.mockReturnValue({
      data: {
        current_version: '1.3.0', latest_version: '1.3.0', update_available: false,
        release_url: null, published_at: null, checked_at: '2026-10-14T09:00:00Z', check_error: null,
        update_supported: true, deployment_mode: 'docker', job: null,
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
        update_supported: true, deployment_mode: 'binary', job: null,
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
