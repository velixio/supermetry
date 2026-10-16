import '@testing-library/jest-dom/vitest'

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const copyText = vi.fn((_value: string) => Promise.resolve(true))
vi.mock('@/lib/clipboard', () => ({ copyText: (value: string) => copyText(value) }))

import { AiInstallPrompt, InstallRecipes, SiteDetails, VerifyInstallation } from './InstallRecipes'

const snippet = '<script defer data-site="abc123def456" src="https://stats.example.com/js/script.js"></script>'

describe('InstallRecipes', () => {
  afterEach(() => { cleanup(); copyText.mockClear() })

  it('offers every platform as a tab and shows the real URL', () => {
    render(<InstallRecipes publicId="abc123def456" snippet={snippet} />)
    const labels = screen.getAllByRole('tab').map((tab) => tab.textContent)
    expect(labels).toEqual(expect.arrayContaining(['HTML', 'Next.js', 'Nuxt', 'SvelteKit', 'Astro', 'Remix', 'Angular', 'WordPress', 'Shopify', 'Google Tag Manager', 'curl', 'Node.js', 'Python', 'Go']))
    expect(screen.getByLabelText('HTML snippet')).toHaveTextContent('https://stats.example.com/js/script.js')
  })

  it('shows the npm install command for package recipes', () => {
    render(<InstallRecipes publicId="abc123def456" snippet={snippet} />)
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Next.js' }), { button: 0, ctrlKey: false })
    expect(screen.getByLabelText('Next.js install command')).toHaveTextContent('npm install @supermetry/nextjs')
    expect(screen.getByLabelText('Next.js snippet')).toHaveTextContent('host="https://stats.example.com"')
  })

  it('switches recipe and copies the visible snippet', async () => {
    render(<InstallRecipes publicId="abc123def456" snippet={snippet} />)
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Python' }), { button: 0, ctrlKey: false })
    fireEvent.click(screen.getByRole('button', { name: 'Copy Python snippet' }))
    await waitFor(() => expect(copyText).toHaveBeenCalledTimes(1))
    expect(copyText.mock.calls[0]?.[0]).toContain('requests.post(')
    expect(await screen.findByText('Copied')).toBeVisible()
  })
})

describe('VerifyInstallation', () => {
  afterEach(cleanup)

  it('waits until an event arrives, then confirms', async () => {
    const verify = vi.fn()
      .mockResolvedValueOnce({ status: 'waiting', lastSeenAt: null })
      .mockResolvedValueOnce({ status: 'receiving', lastSeenAt: new Date().toISOString() })
    render(<VerifyInstallation verify={verify} intervalMs={1} />)
    fireEvent.click(screen.getByRole('button', { name: 'Verify installation' }))
    expect(await screen.findByText(/Waiting for the first event/)).toBeVisible()
    expect(await screen.findByText('Events are arriving. Installation verified.')).toBeVisible()
    expect(verify).toHaveBeenCalledTimes(2)
  })

  it('reports an error without polling forever', async () => {
    const verify = vi.fn().mockRejectedValue(new Error('offline'))
    render(<VerifyInstallation verify={verify} intervalMs={1} />)
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Verify installation' })) })
    expect(await screen.findByText('Could not check the status. Try again.')).toBeVisible()
    expect(verify).toHaveBeenCalledTimes(1)
  })
})

describe('SiteDetails', () => {
  afterEach(() => { cleanup(); copyText.mockClear() })

  it('shows and copies the site id and host', async () => {
    render(<SiteDetails publicId="abc123def456" snippet={snippet} />)
    expect(screen.getByLabelText('Site ID')).toHaveTextContent('abc123def456')
    expect(screen.getByLabelText('Host')).toHaveTextContent('https://stats.example.com')
    fireEvent.click(screen.getByRole('button', { name: 'Copy host' }))
    await waitFor(() => expect(copyText).toHaveBeenCalledWith('https://stats.example.com'))
  })
})

describe('AiInstallPrompt', () => {
  afterEach(() => { cleanup(); copyText.mockClear() })

  it('copies a prompt containing the site id and host', async () => {
    render(<AiInstallPrompt publicId="abc123def456" snippet={snippet} />)
    fireEvent.click(screen.getByRole('button', { name: 'Copy AI prompt' }))
    await waitFor(() => expect(copyText).toHaveBeenCalledTimes(1))
    expect(copyText.mock.calls[0]?.[0]).toContain('Site ID: abc123def456')
    expect(copyText.mock.calls[0]?.[0]).toContain('Host: https://stats.example.com')
  })
})
