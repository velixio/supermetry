import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ImportsExportPage, importPollInterval } from './ImportsExportPage'
import { downloadSiteExport, previewSiteImport, privateRequest, uploadSiteImport } from './privateApi'

vi.mock('./privateApi', () => ({
  privateRequest: vi.fn(),
  uploadSiteImport: vi.fn(),
  previewSiteImport: vi.fn(),
  downloadSiteExport: vi.fn(),
}))

const request = vi.mocked(privateRequest)
const upload = vi.mocked(uploadSiteImport)
const download = vi.mocked(downloadSiteExport)
const preview = vi.mocked(previewSiteImport)

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ImportsExportPage siteId={7} orgId={3} />
    </QueryClientProvider>,
  )
}

describe('imports and export', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('polls while an import is pending or running', () => {
    expect(importPollInterval([])).toBe(false)
    expect(importPollInterval([{ status: 'completed' }])).toBe(false)
    expect(importPollInterval([{ status: 'running' }])).toBe(2_000)
  })

  it('uploads a file through the wizard and shows progress', async () => {
    request.mockResolvedValue([])
    preview.mockResolvedValue({
      source: 'umami', file_name: 'umami.csv', rows: 1, bad_rows: 0, duplicate_rows: 0, errors: [], days: 1,
      date_from: '2024-03-01', date_to: '2024-03-01',
      totals: { visitors: 1, visits: 1, pageviews: 1, events: 0 }, dimensions: {},
      overlap: { imported_days: 0, live_days: 0, same_file: null },
    })
    upload.mockImplementation(async (_siteId, _orgId, _source, _file, onProgress) => {
      onProgress(42)
      return new Promise(() => {})
    })
    renderPage()

    fireEvent.click(screen.getByRole('tab', { name: 'Upload a file' }))
    fireEvent.change(screen.getByLabelText('File type'), { target: { value: 'umami' } })
    const file = new File(['website_id,session_id\n1,a'], 'umami.csv', { type: 'text/csv' })
    fireEvent.change(screen.getByLabelText('Import file'), { target: { files: [file] } })
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Import this range' }))

    expect(await screen.findByText('Uploading 42%')).toBeVisible()
    expect(upload).toHaveBeenCalledWith(7, 3, 'umami', file, expect.any(Function), undefined, false)
  })

  it('lists status and deletes an import', async () => {
    request.mockImplementation(async (method) => {
      if (method === 'GET') {
        return [{
          id: 8,
          site_id: 7,
          source: 'plausible',
          status: 'completed',
          file_name: 'site-export.zip',
          date_from: '2025-01-01',
          date_to: '2025-01-31',
          rows: 120,
          created_by: 2,
          created_at: '2026-10-08T10:00:00Z',
          completed_at: '2026-10-08T10:01:00Z',
        }]
      }
      return undefined as never
    })
    renderPage()

    expect(await screen.findByText('site-export.zip')).toBeVisible()
    expect(screen.getByText('Completed')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Undo import site-export.zip' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Undo import' }))

    await waitFor(() => {
      expect(request).toHaveBeenCalledWith('DELETE', '/api/private/imports/8', {
        orgId: 3,
      })
    })
  })

  it('downloads an export for the selected date range', async () => {
    request.mockResolvedValue([])
    download.mockResolvedValue(undefined)
    renderPage()

    fireEvent.change(screen.getByLabelText('Export from'), {
      target: { value: '2025-02-01' },
    })
    fireEvent.change(screen.getByLabelText('Export to'), {
      target: { value: '2025-02-28' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Download ZIP' }))

    await waitFor(() => {
      expect(download).toHaveBeenCalledWith(7, 3, '2025-02-01', '2025-02-28')
    })
  })
})
