import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ImportsExportPage } from './ImportsExportPage'
import { previewSiteImport, privateRequest, uploadSiteImport } from './privateApi'

vi.mock('./privateApi', () => ({
  privateRequest: vi.fn(),
  uploadSiteImport: vi.fn(),
  previewSiteImport: vi.fn(),
  downloadSiteExport: vi.fn(),
}))

const request = vi.mocked(privateRequest)
const upload = vi.mocked(uploadSiteImport)
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

describe('event export imports', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it.each([
    ['mixpanel', 'events.jsonl', /Needed per event: event, time and distinct_id/],
    ['amplitude', 'events.zip', /Needed per event: event_type, event_time/],
  ] as const)('shows the %s format, previews, then confirms', async (source, name, help) => {
    request.mockResolvedValue([])
    preview.mockResolvedValue({
      source,
      file_name: name,
      rows: 5,
      bad_rows: 1,
      duplicate_rows: 2,
      errors: [{ row: 4, message: 'invalid time "x"' }],
      days: 2,
      date_from: '2024-03-01',
      date_to: '2024-03-02',
      totals: { visitors: 3, visits: 3, pageviews: 4, events: 1 },
      dimensions: { page: 2 },
      overlap: { imported_days: 0, live_days: 0, same_file: null },
    })
    upload.mockResolvedValue({ id: 11, status: 'pending' } as never)
    renderPage()

    fireEvent.change(screen.getByLabelText('Import source'), { target: { value: source } })
    expect(screen.getByText(help)).toBeVisible()
    const file = new File(['{}'], name)
    fireEvent.change(screen.getByLabelText('Import file'), { target: { files: [file] } })
    fireEvent.click(screen.getByRole('button', { name: 'Preview import' }))

    expect(await screen.findByText('Row 4: invalid time "x"')).toBeVisible()
    expect(upload).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm import' }))
    await waitFor(() => {
      expect(upload).toHaveBeenCalledWith(7, 3, source, file, expect.any(Function), undefined, false)
    })
  })
})
