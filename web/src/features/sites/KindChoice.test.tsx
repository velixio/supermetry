import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Onboarding } from './Onboarding'
import type { Me } from '@/routes/app.$org'

afterEach(cleanup)

const me = {
  demo_prompt_dismissed: true,
  memberships: [{ org: { id: 1, slug: 'acme', name: 'Acme' }, role: 'owner', sites: [], site_ids: [], capabilities: [], effective_capabilities: [] }],
} as unknown as Me

const wrap = (node: ReactNode) => <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{node}</QueryClientProvider>

describe('Onboarding kind choice', () => {
  it('creates a pixel project with only name and time zone', async () => {
    const createSite = vi.fn().mockResolvedValue({ id: 9, slug: 'readme', public_id: 'abc', kind: 'pixel' })
    const navigate = vi.fn()
    render(wrap(<Onboarding me={me} createSite={createSite} navigate={navigate} />))
    expect(screen.getByRole('heading', { name: 'What do you want to measure?' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('radio', { name: /Views anywhere/ }))
    expect(screen.queryByLabelText('Domain')).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Project name'), { target: { value: 'README views' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create project' }))
    await waitFor(() => expect(createSite).toHaveBeenCalledWith(expect.objectContaining({ kind: 'pixel', name: 'README views', retention_days: 365 })))
    expect(createSite.mock.calls[0][0]).not.toHaveProperty('hosts')
    expect(navigate).toHaveBeenCalledWith('/app/acme/readme')
  })

  it('keeps the website form as the default', () => {
    render(wrap(<Onboarding me={me} createSite={vi.fn()} navigate={vi.fn()} />))
    expect(screen.getByRole('radio', { name: /Website/ })).toBeChecked()
    expect(screen.getByLabelText('Domain')).toBeInTheDocument()
  })
})
