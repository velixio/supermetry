import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { Onboarding } from './Onboarding'
import * as sitesApi from './api'
import type { Me } from '@/routes/app.$org'

afterEach(cleanup)
beforeEach(() => vi.restoreAllMocks())

const me = {
  demo_prompt_dismissed: true,
  memberships: [{ org: { id: 1, slug: 'acme', name: 'Acme' }, role: 'owner', sites: [], site_ids: [], capabilities: [], effective_capabilities: [] }],
} as unknown as Me

const wrap = (node: ReactNode) => <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{node}</QueryClientProvider>

function start(createSite = vi.fn().mockResolvedValue({ id: 9, slug: 'desk', public_id: 'pub123', kind: 'app' })) {
  const navigate = vi.fn()
  render(wrap(<Onboarding me={me} createSite={createSite} navigate={navigate} />))
  fireEvent.click(screen.getByRole('radio', { name: /App/ }))
  fireEvent.change(screen.getByLabelText('App name'), { target: { value: 'Desk' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create app' }))
  return { createSite, navigate }
}

describe('creating an app', () => {
  it('creates the site, issues a key, shows it once, then navigates on close', async () => {
    const rotate = vi.spyOn(sitesApi, 'rotateAppIngestKey').mockResolvedValue({ key: 'smk_secret', prefix: 'abcd1234', previous_expires_at: null })
    const { createSite, navigate } = start()
    await waitFor(() => expect(screen.getByDisplayValue('smk_secret')).toBeInTheDocument())
    expect(createSite).toHaveBeenCalledWith({ kind: 'app', name: 'Desk', timezone: expect.any(String), retention_days: 90 })
    expect(rotate).toHaveBeenCalledWith(9, 1)
    expect(screen.getByText(/SUPERMETRY_APP_ID=pub123/)).toBeInTheDocument()
    expect(screen.getByText(/Shown once/)).toBeInTheDocument()
    expect(navigate).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(navigate).toHaveBeenCalledWith('/app/acme/desk/settings/app')
  })

  it('offers a retry when the key call fails, without creating the site again', async () => {
    const rotate = vi.spyOn(sitesApi, 'rotateAppIngestKey')
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce({ key: 'smk_second', prefix: 'abcd1234', previous_expires_at: null })
    const { createSite } = start()
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('boom'))
    fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
    await waitFor(() => expect(screen.getByDisplayValue('smk_second')).toBeInTheDocument())
    expect(createSite).toHaveBeenCalledTimes(1)
    expect(rotate).toHaveBeenCalledTimes(2)
  })
})
