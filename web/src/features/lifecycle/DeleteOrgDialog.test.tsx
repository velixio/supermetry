import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

import { DeleteOrgDialog } from './DeleteOrgDialog'

afterEach(cleanup)

function renderDialog(node: ReactNode) {
  render(<QueryClientProvider client={new QueryClient()}>{node}</QueryClientProvider>)
}

describe('DeleteOrgDialog', () => {
  it('explains the grace period and navigates to account after scheduling', async () => {
    const remove = vi.fn().mockResolvedValue({ scheduled: true, purge_at: '2026-10-18T08:00:00Z' })
    const navigate = vi.fn()
    renderDialog(<DeleteOrgDialog orgId={4} orgName="Acme" hasNonDemoSites totpEnabled remove={remove} navigate={navigate} />)

    fireEvent.click(screen.getByRole('button', { name: 'Delete organization' }))
    expect(screen.getByText(/restore it for 7 days/i)).toBeVisible()
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'secret' } })
    fireEvent.change(screen.getByLabelText('Authentication code'), { target: { value: '123456' } })
    fireEvent.click(screen.getByRole('button', { name: 'Confirm deletion' }))

    await waitFor(() => expect(remove).toHaveBeenCalledWith(4, { password: 'secret', totp_code: '123456' }))
    expect(navigate).toHaveBeenCalledWith('/app/account')
  })

  it('explains immediate deletion and navigates home', async () => {
    const remove = vi.fn().mockResolvedValue({ scheduled: false, purge_at: null })
    const navigate = vi.fn()
    renderDialog(<DeleteOrgDialog orgId={4} orgName="Acme" hasNonDemoSites={false} totpEnabled={false} remove={remove} navigate={navigate} />)

    fireEvent.click(screen.getByRole('button', { name: 'Delete organization' }))
    expect(screen.getByText(/deleted now/i)).toBeVisible()
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'secret' } })
    fireEvent.click(screen.getByRole('button', { name: 'Confirm deletion' }))

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/app'))
  })
})
