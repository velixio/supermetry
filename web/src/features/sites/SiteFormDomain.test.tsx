import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'

import { SiteForm, firstNonDomainHost } from './SiteForm'

afterEach(cleanup)

describe('firstNonDomainHost', () => {
  it('flags single-label hosts but not domains, wildcards, localhost or IPs', () => {
    expect(firstNonDomainHost(['example.com', '*.example.com', 'localhost', '127.0.0.1'])).toBeUndefined()
    expect(firstNonDomainHost(['example.com', 'afaridar'])).toBe('afaridar')
    expect(firstNonDomainHost(['*.intranet'])).toBe('*.intranet')
  })
})

describe('SiteForm on a managed instance', () => {
  beforeAll(() => {
    Element.prototype.scrollIntoView ??= vi.fn()
  })

  it('shows the domain hint and blocks a single-label host before calling the server', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    render(<SiteForm submitLabel="Add site" onSubmit={onSubmit} requireDomain />)
    expect(screen.getByText(/with a dot, such as example\.com/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'afaridar' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add site' }))
    expect(await screen.findByText('host "afaridar" is not a domain: use something like example.com')).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('accepts a real domain', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    render(<SiteForm submitLabel="Add site" onSubmit={onSubmit} requireDomain />)
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add site' }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalled())
  })
})

describe('SiteForm on a self-hosted instance', () => {
  it('has no extra hint and still sends a single-label host', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    render(<SiteForm submitLabel="Add site" onSubmit={onSubmit} />)
    expect(screen.queryByText(/with a dot, such as example\.com/)).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'wiki' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add site' }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ hosts: ['wiki'] })))
  })
})
