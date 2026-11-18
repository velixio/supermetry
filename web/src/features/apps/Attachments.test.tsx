import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Attachments, attachmentURL, isInlineImage } from './Attachments'

const source = { siteId: 7, eventId: 'evt 1' }

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('Attachments', () => {
  it('builds the download URL from the site, event and list position', () => {
    expect(attachmentURL(7, 'evt 1', 2)).toBe('/api/private/sites/7/app/events/evt%201/attachments/2')
  })

  it('shows only png, jpeg and webp as images', () => {
    expect(['image/png', 'image/jpeg', 'image/webp'].every(isInlineImage)).toBe(true)
    expect(['image/svg+xml', 'text/html', 'image/gif', 'text/plain', undefined].some(isInlineImage)).toBe(false)
  })

  it('renders image thumbnails and download links for stored files', () => {
    render(<Attachments source={source} attachments={[
      { name: 'shot.png', content_type: 'image/png', size: 2048, stored: true },
      { name: 'dump.bin', content_type: 'application/octet-stream', size: 10, stored: true },
    ]} />)
    const img = screen.getByRole('img', { name: 'shot.png' })
    expect(img).toHaveAttribute('src', '/api/private/sites/7/app/events/evt%201/attachments/0')
    const links = screen.getAllByRole('link', { name: /download/i })
    expect(links[0]).toHaveAttribute('href', expect.stringMatching(/attachments\/0$/))
    expect(links[0]).toHaveAttribute('download', 'shot.png')
    expect(links[1]).toHaveAttribute('href', expect.stringMatching(/attachments\/1$/))
    // Anything that is not a safe image is never an <img>.
    expect(screen.getAllByRole('img')).toHaveLength(1)
  })

  it('loads a text file only when its viewer is opened and shows it as plain text', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('line one\n<script>alert(1)</script>', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    render(<Attachments source={source} attachments={[{ name: 'log.txt', content_type: 'text/plain', size: 30, stored: true }]} />)
    expect(fetchMock).not.toHaveBeenCalled()
    fireEvent.click(screen.getByText('View log.txt'))
    await waitFor(() => expect(screen.getByText(/line one/)).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledWith('/api/private/sites/7/app/events/evt%201/attachments/0', expect.objectContaining({ credentials: 'same-origin' }))
    expect(screen.getByText(/<script>alert\(1\)<\/script>/)).toBeInTheDocument()
    expect(document.querySelector('script')).toBeNull()
  })

  it('says when a file was not stored and offers no link', () => {
    render(<Attachments source={source} attachments={[
      { name: 'big.txt', content_type: 'text/plain', size: 3 * 1024 * 1024, stored: false },
      { name: 'old.log', size: 5 },
    ]} />)
    expect(screen.getAllByText('Not stored')).toHaveLength(2)
    expect(screen.queryByRole('link')).toBeNull()
    expect(screen.getByText(/3\.0 MiB/)).toBeInTheDocument()
  })

  it('shows an error when the text cannot be loaded', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('nope', { status: 404 })))
    render(<Attachments source={source} attachments={[{ name: 'log.txt', content_type: 'text/plain', size: 3, stored: true }]} />)
    fireEvent.click(screen.getByText('View log.txt'))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/could not load/i))
  })
})
