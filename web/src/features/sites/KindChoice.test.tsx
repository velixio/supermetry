import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Onboarding } from './Onboarding'
import type { Me } from '@/routes/app.$org'

afterEach(cleanup)

const me = {
  demo_prompt_dismissed: true,
  memberships: [{ org: { id: 1, slug: 'acme', name: 'Acme' }, role: 'owner', sites: [], site_ids: [], capabilities: [], effective_capabilities: [] }],
} as unknown as Me

describe('Onboarding kind choice', () => {
  it('creates a pixel project with only name and time zone', async () => {
    const createSite = vi.fn().mockResolvedValue({ id: 9, slug: 'readme', public_id: 'abc', kind: 'pixel' })
    const navigate = vi.fn()
    render(<Onboarding me={me} createSite={createSite} navigate={navigate} />)
    expect(screen.getByRole('heading', { name: 'What do you want to measure?' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('radio', { name: /Views anywhere/ }))
    expect(screen.queryByLabelText('Domains')).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Project name'), { target: { value: 'README views' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create project' }))
    await waitFor(() => expect(createSite).toHaveBeenCalledWith(expect.objectContaining({ kind: 'pixel', name: 'README views', retention_days: 365 })))
    expect(createSite.mock.calls[0][0]).not.toHaveProperty('hosts')
    expect(navigate).toHaveBeenCalledWith('/app/acme/readme')
  })

  it('keeps the website form as the default', () => {
    render(<Onboarding me={me} createSite={vi.fn()} navigate={vi.fn()} />)
    expect(screen.getByRole('radio', { name: /Website/ })).toBeChecked()
    expect(screen.getByLabelText('Domains')).toBeInTheDocument()
  })
})
