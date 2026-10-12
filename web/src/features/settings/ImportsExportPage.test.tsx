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

  it('uploads the selected source and shows progress', async () => {
    request.mockResolvedValue([])
    upload.mockImplementation(async (_siteId, _orgId, _source, _file, onProgress) => {
      onProgress(42)
      return { id: 9, status: 'pending' } as never
    })
    renderPage()

    fireEvent.change(screen.getByLabelText('Import source'), {
      target: { value: 'umami' },
    })
    const file = new File(['website_id,session_id\n1,a'], 'umami.csv', {
      type: 'text/csv',
    })
    fireEvent.change(screen.getByLabelText('Import file'), {
      target: { files: [file] },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Upload import' }))

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

  it('previews a GA4 file before importing and replaces a repeated file', async () => {
    request.mockResolvedValue([])
    preview.mockResolvedValue({
      source: 'ga4',
      file_name: 'ga4.csv',
      rows: 3,
      bad_rows: 1,
      duplicate_rows: 0,
      errors: [{ row: 6, message: 'invalid date "x"' }],
      days: 3,
      date_from: '2024-03-01',
      date_to: '2024-03-03',
      totals: { visitors: 1380, visits: 450, pageviews: 1150, events: 2400 },
      dimensions: { page: 2 },
      overlap: {
        imported_days: 2,
        live_days: 1,
        same_file: { import_id: 4, imported_at: '2026-10-01T00:00:00Z' },
      },
    })
    upload.mockResolvedValue({ id: 10, status: 'pending' } as never)
    renderPage()

    fireEvent.change(screen.getByLabelText('Import source'), { target: { value: 'ga4' } })
    const file = new File(['Date,Sessions\n20240301,1'], 'ga4.csv', { type: 'text/csv' })
    fireEvent.change(screen.getByLabelText('Import file'), { target: { files: [file] } })
    fireEvent.click(screen.getByRole('button', { name: 'Preview import' }))

    expect(await screen.findByText('Row 6: invalid date "x"')).toBeVisible()
    expect(screen.getByText('1,380')).toBeVisible()
    expect(screen.getByText('2024-03-01 – 2024-03-03')).toBeVisible()
    expect(upload).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Replace earlier import' }))
    await waitFor(() => {
      expect(upload).toHaveBeenCalledWith(7, 3, 'ga4', file, expect.any(Function), undefined, true)
    })
  })
})
