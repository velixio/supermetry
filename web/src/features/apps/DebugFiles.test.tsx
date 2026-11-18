import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({ Link: ({ children, to }: { children: React.ReactNode; to: string }) => <a href={to}>{children}</a> }))
const { privateRequest } = vi.hoisted(() => ({ privateRequest: vi.fn() }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))

import { DebugFiles } from './DebugFiles'
import { EventBodyView } from './EventDetail'
import { renderWithQuery } from './testing'

const listing = {
  files: [
    { id: 1, kind: 'elf', debug_id: '7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01', code_id: '', arch: 'arm64', object_name: 'libfixture.so', features: ['symtab'], cache_state: 'ok', size: 2048, created_at: '2026-11-18T10:00:00Z', org_wide: false },
    { id: 2, kind: 'proguard', debug_id: '6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b', code_id: '', arch: '', object_name: 'mapping.txt', features: [], cache_state: 'ok', size: 5, created_at: '2026-11-18T10:00:00Z', org_wide: true },
  ],
  missing: [{ kind: 'macho', debug_id: 'de7fada3-56f7-3ce7-b81e-0b6e964459c1', code_file: 'Fix', arch: 'arm64', last_seen: '2026-11-18T09:00:00Z', events: 7 }],
}

describe('DebugFiles', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    privateRequest.mockImplementation((method: string) => Promise.resolve(method === 'GET' ? listing : undefined))
  })
  afterEach(cleanup)

  it('lists the missing images and the uploaded files', async () => {
    renderWithQuery(<DebugFiles siteId={7} role="owner" />)
    const missing = await screen.findByRole('table', { name: 'Missing symbols' })
    expect(within(missing).getByText('Fix')).toBeInTheDocument()
    expect(within(missing).getByText('de7fada3…')).toBeInTheDocument()
    const files = screen.getByRole('table', { name: 'Debug files' })
    expect(within(files).getByText('libfixture.so')).toBeInTheDocument()
    expect(within(files).getByText('org-wide')).toBeInTheDocument()
    expect(privateRequest.mock.calls[0][1]).toBe('/api/private/sites/7/app/debug-files')
  })

  it('an editor deletes the project file but is not offered the org-wide one; a viewer deletes nothing', async () => {
    renderWithQuery(<DebugFiles siteId={7} role="editor" />)
    await screen.findByRole('table', { name: 'Debug files' })
    expect(screen.getAllByRole('button', { name: /^Delete / })).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Delete libfixture.so' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Delete file' }))
    await waitFor(() => expect(privateRequest).toHaveBeenCalledWith('DELETE', '/api/private/sites/7/app/debug-files/1'))
    cleanup()
    renderWithQuery(<DebugFiles siteId={7} role="viewer" />)
    await screen.findByRole('table', { name: 'Debug files' })
    expect(screen.queryByRole('button', { name: /^Delete / })).not.toBeInTheDocument()
    cleanup()
    renderWithQuery(<DebugFiles siteId={7} role="admin" />)
    await screen.findByRole('table', { name: 'Debug files' })
    expect(screen.getAllByRole('button', { name: /^Delete / })).toHaveLength(2)
  })
})

describe('missing symbols banner', () => {
  const body = { release: '1.0', exceptions: [{ type: 'SIGSEGV', frames: [{ function: 'a' }] }] }
  afterEach(cleanup)

  it('names the missing native debug file and the missing ProGuard mapping', () => {
    render(<EventBodyView body={{ ...body, symbolication: 'missing', missing_debug: [
      { kind: 'elf', debug_id: '7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01', code_file: 'libfixture.so' },
      { kind: 'proguard', debug_id: '6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b' },
    ] }} />)
    const banner = screen.getByRole('status', { name: 'Missing symbols' })
    expect(banner).toHaveTextContent('Missing debug file for libfixture.so (7a3c9e10…). Upload it with sentry-cli debug-files upload.')
    expect(banner).toHaveTextContent('Missing ProGuard mapping 6a1b8c0e…')
    expect(screen.queryByRole('status', { name: 'Source maps' })).not.toBeInTheDocument()
  })

  it('keeps the source map banner for events without missing images and shows nothing when resolved', () => {
    render(<EventBodyView body={{ ...body, symbolication: 'missing' }} />)
    expect(screen.getByRole('status', { name: 'Source maps' })).toBeInTheDocument()
    cleanup()
    render(<EventBodyView body={{ ...body, symbolication: 'ok', missing_debug: [{ kind: 'elf', debug_id: 'x' }] }} />)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
})
