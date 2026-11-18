import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { privateRequest } from '../privateApi'
import { ExportsPage, scheduleChip } from './ExportsPage'
import { SettingsBundle } from './SettingsBundle'
import { draftOf, scheduleBody, type ExportSchedule } from './types'

vi.mock('../privateApi', () => ({ privateRequest: vi.fn() }))
const request = vi.mocked(privateRequest)

beforeAll(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  HTMLElement.prototype.scrollIntoView = vi.fn()
  HTMLElement.prototype.hasPointerCapture = vi.fn(() => false)
})

const schedule: ExportSchedule = {
  id: 5, site_id: null, name: 'Nightly', frequency: 'daily', incremental: true, enabled: true,
  endpoint: 'https://s3.example.test', region: 'eu', bucket: 'stats', prefix: 'sm', access_key_id: 'AKIA', secret_set: true, path_style: false,
  next_run_at: '2026-11-19T03:00:00Z', last_run_at: '2026-11-18T03:00:00Z', last_status: 'failed', last_error: 'access denied',
}

function wrap(node: React.ReactNode) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>{node}</QueryClientProvider>)
}

beforeEach(() => {
  request.mockReset()
})
afterEach(cleanup)

describe('export schedule helpers', () => {
  it('maps status chips', () => {
    expect(scheduleChip({ enabled: true, last_status: 'succeeded' }).variant).toBe('success')
    expect(scheduleChip({ enabled: true, last_status: 'failed' }).variant).toBe('danger')
    expect(scheduleChip({ enabled: true, last_status: 'skipped' }).variant).toBe('warning')
    expect(scheduleChip({ enabled: false, last_status: 'succeeded' }).label).toBe('Paused')
    expect(scheduleChip({ enabled: true, last_status: null }).label).toBe('Not run yet')
  })

  it('keeps the stored secret when the edit form leaves it empty', () => {
    const draft = draftOf(schedule)
    expect(draft.secret).toBe('')
    expect(scheduleBody(draft, 'edit')).not.toHaveProperty('secret')
    expect(scheduleBody({ ...draft, secret: 'new' }, 'edit')).toHaveProperty('secret', 'new')
    expect(scheduleBody(draft, 'edit')).not.toHaveProperty('site_id')
  })
})

describe('ExportsPage', () => {
  it('blocks non-managers', () => {
    wrap(<ExportsPage orgId={1} role="viewer" sites={[]} />)
    expect(screen.getByRole('alert')).toHaveTextContent('Only owners and admins can manage exports.')
    expect(request).not.toHaveBeenCalled()
  })

  it('lists schedules with chips and queues a run', async () => {
    request.mockImplementation(async (method, path) => {
      if (method === 'GET') return { schedules: [schedule] } as never
      return {} as never
    })
    wrap(<ExportsPage orgId={1} role="owner" sites={[]} />)
    const table = await screen.findByRole('table', { name: 'Scheduled exports' })
    expect(table).toHaveTextContent('Nightly')
    expect(table).toHaveTextContent('Failed')
    expect(table).toHaveTextContent('access denied')
    fireEvent.pointerDown(screen.getByRole('button', { name: 'Actions for Nightly' }), { button: 0, ctrlKey: false, pointerType: 'mouse' })
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Run now' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('POST', '/api/private/orgs/1/export-schedules/5/run', expect.anything()))
    expect(await screen.findByText('Export queued.')).toBeVisible()
  })

  it('edit form never shows the secret, omits it when empty, and tests the connection', async () => {
    request.mockImplementation(async (method, path) => {
      if (method === 'GET') return { schedules: [schedule] } as never
      if (String(path).endsWith('/test')) return { ok: false, error: 'bad key' } as never
      return {} as never
    })
    wrap(<ExportsPage orgId={1} role="admin" sites={[]} />)
    await screen.findByRole('table')
    fireEvent.pointerDown(screen.getByRole('button', { name: 'Actions for Nightly' }), { button: 0, ctrlKey: false, pointerType: 'mouse' })
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Edit' }))
    const dialog = await screen.findByRole('dialog', { name: 'Edit scheduled export' })
    const secret = within(dialog).getByLabelText('Secret access key')
    expect(secret).toHaveValue('')
    expect(secret).toHaveAttribute('type', 'password')
    expect(dialog).toHaveTextContent('A secret is stored. Leave this empty to keep it.')

    fireEvent.click(within(dialog).getByRole('button', { name: 'Test connection' }))
    expect(await within(dialog).findByText('Connection failed: bad key')).toBeVisible()

    fireEvent.click(within(dialog).getByRole('button', { name: 'Save schedule' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('PATCH', '/api/private/orgs/1/export-schedules/5', expect.objectContaining({ body: expect.not.objectContaining({ secret: expect.anything() }) })))
  })

  it('create form requires a secret and disables the connection test until saved', async () => {
    request.mockResolvedValue({ schedules: [] } as never)
    wrap(<ExportsPage orgId={1} role="owner" sites={[]} />)
    fireEvent.click(await screen.findByRole('button', { name: 'New schedule' }))
    const dialog = await screen.findByRole('dialog', { name: 'New scheduled export' })
    expect(within(dialog).getByRole('button', { name: 'Test connection' })).toBeDisabled()
    for (const [label, value] of [['Name', 'Weekly'], ['Region', 'eu'], ['Bucket', 'b'], ['Access key ID', 'AK']] as const) {
      fireEvent.change(within(dialog).getByLabelText(label), { target: { value } })
    }
    fireEvent.change(within(dialog).getByLabelText('Endpoint'), { target: { value: 'https://s3.example.test' } })
    fireEvent.submit(within(dialog).getByRole('button', { name: 'Save schedule' }).closest('form')!)
    expect(await within(dialog).findByText('Enter the secret access key')).toBeVisible()
    fireEvent.change(within(dialog).getByLabelText('Secret access key'), { target: { value: 's3cret' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save schedule' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('POST', '/api/private/orgs/1/export-schedules', expect.objectContaining({ body: expect.objectContaining({ secret: 's3cret', name: 'Weekly' }) })))
  })
})

describe('SettingsBundle', () => {
  const report = { dry_run: true, sites: [{ name: 'Docs', slug: 'docs', action: 'create', goals: 2 }], members: [{ email: 'a@example.test', role: 'viewer', action: 'would_invite' }], warnings: ['Share links are not recreated'] }

  it('links the download', () => {
    wrap(<SettingsBundle orgId={4} />)
    expect(screen.getByRole('link', { name: 'Download settings bundle' })).toHaveAttribute('href', '/api/private/orgs/4/settings-export')
  })

  it('runs a dry run first, then applies only after confirmation', async () => {
    request.mockResolvedValue(report as never)
    wrap(<SettingsBundle orgId={4} />)
    const file = new File([JSON.stringify({ version: 1 })], 'bundle.json', { type: 'application/json' })
    fireEvent.change(screen.getByLabelText('Import settings bundle'), { target: { files: [file] } })
    const region = await screen.findByRole('region', { name: 'What would change' })
    expect(region).toHaveTextContent('Docs: create, 2 goals')
    expect(region).toHaveTextContent('a@example.test')
    expect(region).toHaveTextContent('Share links are not recreated')
    expect(request).toHaveBeenCalledTimes(1)
    expect(request).toHaveBeenCalledWith('POST', '/api/private/orgs/4/settings-import?dry_run=true&invite_members=false', expect.objectContaining({ body: { version: 1 } }))

    fireEvent.click(screen.getByRole('checkbox', { name: /Invite the bundle/ }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('POST', '/api/private/orgs/4/settings-import?dry_run=true&invite_members=true', expect.anything()))
    fireEvent.click(await screen.findByRole('button', { name: 'Apply import' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('POST', '/api/private/orgs/4/settings-import?dry_run=false&invite_members=true', expect.anything()))
    expect(await screen.findByText('Import finished.')).toBeVisible()
  })

  it('rejects a file that is not JSON without calling the server', async () => {
    wrap(<SettingsBundle orgId={4} />)
    fireEvent.change(screen.getByLabelText('Import settings bundle'), { target: { files: [new File(['nope'], 'x.json')] } })
    expect(await screen.findByRole('alert')).toHaveTextContent('not a valid settings bundle')
    expect(request).not.toHaveBeenCalled()
  })
})
