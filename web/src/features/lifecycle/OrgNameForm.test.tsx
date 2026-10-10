import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'

import { OrgNameForm } from './OrgNameForm'

afterEach(cleanup)

function renderForm(node: ReactNode) {
  render(<QueryClientProvider client={new QueryClient()}>{node}</QueryClientProvider>)
}

describe('OrgNameForm', () => {
  it('validates and saves a trimmed organization name', async () => {
    const rename = vi.fn().mockResolvedValue({ id: 4, name: 'North star', slug: 'acme' })
    renderForm(<OrgNameForm orgId={4} name="Acme" role="admin" rename={rename} />)

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: '   ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(await screen.findByText('Enter an organization name.')).toBeVisible()
    expect(rename).not.toHaveBeenCalled()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: '  North star  ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(rename).toHaveBeenCalledWith(4, 'North star'))
  })

  it('shows the name as read-only for a viewer', () => {
    renderForm(<OrgNameForm orgId={4} name="Acme" role="viewer" rename={vi.fn()} />)
    expect(screen.getByText('Acme')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Save changes' })).not.toBeInTheDocument()
  })

  it.each([
    ['invalid_name', 'Enter a valid organization name.'],
    ['unexpected', 'The organization could not be renamed.'],
  ])('shows %s server errors inline', async (code, message) => {
    const rename = vi.fn().mockRejectedValue(new Error(code))
    renderForm(<OrgNameForm orgId={4} name="Acme" role="owner" rename={rename} />)
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'New name' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(message)
  })

  it('counts Unicode code points for the 100-character limit', async () => {
    const rename = vi.fn().mockResolvedValue({ id: 4, name: 'ok', slug: 'acme' })
    renderForm(<OrgNameForm orgId={4} name="Acme" role="owner" rename={rename} />)
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: '😀'.repeat(100) } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(rename).toHaveBeenCalled())
  })
})
