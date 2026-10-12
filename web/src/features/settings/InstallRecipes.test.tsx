import '@testing-library/jest-dom/vitest'

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const copyText = vi.fn((_value: string) => Promise.resolve(true))
vi.mock('@/lib/clipboard', () => ({ copyText: (value: string) => copyText(value) }))

import { InstallRecipes, VerifyInstallation } from './InstallRecipes'

const snippet = '<script defer data-site="abc123def456" src="https://stats.example.com/js/script.js"></script>'

describe('InstallRecipes', () => {
  afterEach(() => { cleanup(); copyText.mockClear() })

  it('offers every platform as a tab and shows the real URL', () => {
    render(<InstallRecipes publicId="abc123def456" snippet={snippet} />)
    const labels = screen.getAllByRole('tab').map((tab) => tab.textContent)
    expect(labels).toEqual(expect.arrayContaining(['HTML', 'Next.js', 'Nuxt', 'SvelteKit', 'Astro', 'Remix', 'Angular', 'WordPress', 'Shopify', 'Google Tag Manager', 'curl', 'Node.js', 'Python', 'Go']))
    expect(screen.getByLabelText('HTML snippet')).toHaveTextContent('https://stats.example.com/js/script.js')
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
