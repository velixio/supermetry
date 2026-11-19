import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { previewSiteImport, privateRequest, uploadSiteImport } from '@/features/settings/privateApi'

import { ImportWizard, type WizardSearch } from './ImportWizard'
import { SwitchOverPanel } from './SwitchOverPanel'

vi.mock('@/features/settings/privateApi', () => ({
  privateRequest: vi.fn(),
  uploadSiteImport: vi.fn(),
  previewSiteImport: vi.fn(),
  downloadSiteExport: vi.fn(),
}))

const request = vi.mocked(privateRequest)
const upload = vi.mocked(uploadSiteImport)
const filePreview = vi.mocked(previewSiteImport)

const preview = {
  source: 'plausible', file_name: 'plausible_api', rows: 90, bad_rows: 0, duplicate_rows: 0, errors: [], days: 90,
  date_from: '2026-01-01', date_to: '2026-03-31',
  totals: { visitors: 5000, visits: 6000, pageviews: 9000, events: 12 },
  dimensions: { page: 40, event: 3, source: 0 },
  overlap: { imported_days: 0, live_days: 0, same_file: null },
}

const capabilities = {
  plausible_api: { available: true },
  matomo_api: { available: false, reason: 'self-hosted installs only' },
  ga4_api: { available: true },
}

type Handler = (method: string, path: string, body?: unknown) => unknown
let routes: Handler

function mockApi(extra: Handler = () => undefined) {
  routes = extra
  request.mockImplementation(async (method, path, options) => {
    const body = (options as { body?: unknown } | undefined)?.body
    const custom = routes(method, path, body)
    if (custom !== undefined) return custom as never
    if (path.endsWith('/capabilities')) return capabilities as never
    if (method === 'GET' && /\/imports$/.test(path)) return [] as never
    return undefined as never
  })
}

function renderWizard(props: { search?: WizardSearch; onSearchChange?: (p: WizardSearch) => void } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <ImportWizard siteId={7} orgId={3} {...props} />
    </QueryClientProvider>,
  )
}

function fillPlausible() {
  fireEvent.change(screen.getByLabelText('Site in Plausible'), { target: { value: 'example.com' } })
  fireEvent.change(screen.getByLabelText('Plausible API key'), { target: { value: 'key-1' } })
}

async function toPreview() {
  fireEvent.click(await screen.findByRole('button', { name: 'Next' }))
  fireEvent.click(screen.getByRole('button', { name: 'Next' }))
}

describe('import wizard', () => {
  beforeEach(() => window.sessionStorage.clear())
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
    vi.useRealTimers()
  })

  it('step 1: shows capability reasons and blocks Next until the source is complete', async () => {
    mockApi()
    renderWizard()
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
    await waitFor(() => expect(screen.getByRole('option', { name: 'Matomo' })).toBeDisabled())
    fillPlausible()
    expect(screen.getByRole('button', { name: 'Next' })).toBeEnabled()
    expect(screen.getByText(/Step 1 of 5/)).toBeInTheDocument()
  })

  it('step 1: explains an unavailable service', async () => {
    mockApi(() => undefined)
    request.mockImplementation(async (method, path) => {
      if (path.endsWith('/capabilities')) return { ...capabilities, plausible_api: { available: false, reason: 'turned off by the host' } } as never
      return (method === 'GET' ? [] : undefined) as never
    })
    renderWizard()
    expect(await screen.findByText('Not available: turned off by the host')).toBeVisible()
  })

  it('step 2: validates the date range', async () => {
    mockApi()
    renderWizard()
    fillPlausible()
    fireEvent.click(await screen.findByRole('button', { name: 'Next' }))
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-05-01' } })
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '2026-04-01' } })
    expect(screen.getByRole('alert')).toHaveTextContent('Choose valid dates')
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
    expect(screen.getByText('Goals and custom events')).toBeVisible()
  })

  it('step 3: previews the pull, shows overlap and starts the import with the same body', async () => {
    const onSearchChange = vi.fn()
    mockApi((method, path) => {
      if (path.endsWith('/pull/preview')) return { ...preview, overlap: { imported_days: 10, live_days: 4, same_file: null } }
      if (method === 'POST' && path.endsWith('/pull')) return { id: 21, status: 'pending', rows: 0, file_name: 'x', source: 'plausible' }
      if (path === '/api/private/imports/21') return { id: 21, status: 'pending', rows: 0, file_name: 'x', source: 'plausible' }
      return undefined
    })
    renderWizard({ onSearchChange })
    fillPlausible()
    await toPreview()

    expect(await screen.findByText('Preview: nothing has been imported yet')).toBeVisible()
    expect(screen.getByText('5,000')).toBeVisible()
    expect(screen.getByText(/10 of these days are already covered by an earlier import/)).toBeVisible()
    expect(screen.getByText(/4 of these days already have data Supermetry collected itself/)).toBeVisible()
    expect(screen.getByText('Goals and custom events')).toBeVisible()
    // Earlier imports overlap, so the button says it replaces them.
    fireEvent.click(screen.getByRole('button', { name: 'Replace the earlier import' }))
    await waitFor(() => expect(onSearchChange).toHaveBeenCalledWith({ import: 21 }))
    const post = request.mock.calls.find(([m, path]) => m === 'POST' && path === '/api/private/sites/7/imports/pull')
    expect(post?.[2]).toMatchObject({ body: { method: 'plausible_api', remote_site_id: 'example.com', api_key: 'key-1' } })
    expect(await screen.findByRole('progressbar', { name: 'Import progress' })).toBeVisible()
  })

  it('step 3: previews only the latest 120 days of a long range and says so', async () => {
    mockApi((_m, path) => (path.endsWith('/pull/preview') ? preview : undefined))
    renderWizard()
    fillPlausible()
    fireEvent.click(await screen.findByRole('button', { name: 'Next' }))
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2025-01-01' } })
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '2025-12-31' } })
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    expect(await screen.findByText(/most recent 120 of 365 days/)).toBeVisible()
    const call = request.mock.calls.find(([, path]) => String(path).endsWith('/pull/preview'))
    expect(call?.[2]).toMatchObject({ body: { from: '2025-09-03', to: '2025-12-31' } })
  })

  it('step 3: "Change the dates" returns to the range step', async () => {
    mockApi((_m, path) => (path.endsWith('/pull/preview') ? preview : undefined))
    renderWizard()
    fillPlausible()
    await toPreview()
    fireEvent.click(await screen.findByRole('button', { name: 'Change the dates' }))
    expect(screen.getByLabelText('From')).toBeVisible()
  })

  it('step 3: a failed preview shows the error and can retry', async () => {
    let calls = 0
    mockApi((_m, path) => {
      if (!path.endsWith('/pull/preview')) return undefined
      calls += 1
      if (calls === 1) throw new Error('Plausible rejected the key')
      return preview
    })
    renderWizard()
    fillPlausible()
    await toPreview()
    expect(await screen.findByText('Plausible rejected the key')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByText('5,000')).toBeVisible()
  })

  it('step 4: shows progress from the server, polls, and offers review at the end', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    let status = 'running'
    const item = () => ({
      id: 30, site_id: 7, source: 'plausible', status, file_name: 'plausible_api', rows: 400,
      date_from: '2026-01-01', date_to: '2026-01-31', created_at: '2026-02-01T00:00:00Z',
      progress: { total_days: 31, done_days: status === 'completed' ? 31 : 10, rows: 400, bad_rows: 0, cursor_day: null, updated_at: '' },
    })
    mockApi((_m, path) => (path === '/api/private/imports/30' ? item() : undefined))
    renderWizard({ search: { import: 30 } })

    const bar = await screen.findByRole('progressbar', { name: 'Import progress' })
    await waitFor(() => expect(bar).toHaveAttribute('aria-valuenow', '32'))
    expect(screen.getByText(/10 of 31 days, 400 rows so far/)).toBeVisible()
    expect(screen.getByText(/You can leave this page/)).toBeVisible()

    status = 'completed'
    await act(async () => { await vi.advanceTimersByTimeAsync(2_100) })
    expect(await screen.findByRole('button', { name: 'Review the result' })).toBeVisible()
    expect(screen.getByRole('progressbar', { name: 'Import progress' })).toHaveAttribute('aria-valuenow', '100')
  })

  it('step 4: a failed import shows the error and no review button', async () => {
    mockApi((_m, path) => (path === '/api/private/imports/31'
      ? { id: 31, site_id: 7, source: 'matomo', status: 'failed', file_name: 'm', rows: 0, error: 'token rejected', created_at: '' }
      : undefined))
    renderWizard({ search: { import: 31 } })
    expect(await screen.findByText(/The import failed. token rejected/)).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Review the result' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Start over' })).toBeVisible()
  })

  it('step 4: resumes an import that is still running after a reload', async () => {
    mockApi((method, path) => {
      if (method === 'GET' && path === '/api/private/sites/7/imports') {
        return [{ id: 40, site_id: 7, source: 'plausible', status: 'running', file_name: 'p', rows: 0, created_at: '' }]
      }
      if (path === '/api/private/imports/40') return { id: 40, site_id: 7, source: 'plausible', status: 'running', file_name: 'p', rows: 0, created_at: '' }
      return undefined
    })
    const onSearchChange = vi.fn()
    renderWizard({ onSearchChange })
    expect(await screen.findByRole('progressbar', { name: 'Import progress' })).toBeVisible()
    expect(onSearchChange).toHaveBeenCalledWith({ import: 40 })
  })

  it('step 5 and 6: reviews monthly totals with tolerance badges, then undoes after confirming', async () => {
    mockApi((method, path) => {
      if (path === '/api/private/imports/50') {
        return { id: 50, site_id: 7, source: 'plausible', status: 'completed', file_name: 'plausible_api', rows: 62, date_from: '2026-01-30', date_to: '2026-02-01', created_at: '' }
      }
      if (path.includes('/imports/compare')) {
        return {
          from: '2026-01-30', to: '2026-02-01',
          days: [
            { day: '2026-01-30', imported: { visitors: 100, visits: 100, pageviews: 200 }, native: { visitors: 101, visits: 100, pageviews: 202 }, visitors_diff_pct: 1, pageviews_diff_pct: 1 },
            { day: '2026-02-01', imported: { visitors: 100, visits: 100, pageviews: 200 }, native: { visitors: 60, visits: 60, pageviews: 120 }, visitors_diff_pct: -40, pageviews_diff_pct: -40 },
          ],
        }
      }
      if (method === 'DELETE') return null
      return undefined
    })
    const onSearchChange = vi.fn()
    renderWizard({ search: { import: 50 }, onSearchChange })
    fireEvent.click(await screen.findByRole('button', { name: 'Review the result' }))

    const table = await screen.findByRole('table')
    const january = within(table).getByRole('row', { name: /2026-01/ })
    expect(within(january).getByText(/Within tolerance/)).toBeVisible()
    const february = within(table).getByRole('row', { name: /2026-02/ })
    expect(within(february).getByText(/Differs/)).toBeVisible()
    expect(within(february).getByText(/−40.0%/)).toBeVisible()

    fireEvent.click(screen.getByRole('button', { name: 'Undo import' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(request).not.toHaveBeenCalledWith('DELETE', expect.anything(), expect.anything())
    fireEvent.click(within(dialog).getByRole('button', { name: 'Undo import' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('DELETE', '/api/private/imports/50', { orgId: 3 }))
    await waitFor(() => expect(onSearchChange).toHaveBeenCalledWith({ import: undefined }))
  })

  it('step 5: says so when nothing can be compared', async () => {
    mockApi((_m, path) => {
      if (path === '/api/private/imports/51') return { id: 51, site_id: 7, source: 'ga4', status: 'completed', file_name: 'g', rows: 3, date_from: '2025-01-01', date_to: '2025-01-03', created_at: '' }
      if (path.includes('/imports/compare')) return { from: '', to: '', days: [{ day: '2025-01-01', imported: { visitors: 1, visits: 1, pageviews: 1 }, native: null, visitors_diff_pct: null, pageviews_diff_pct: null }] }
      return undefined
    })
    renderWizard({ search: { import: 51 } })
    fireEvent.click(await screen.findByRole('button', { name: 'Review the result' }))
    expect(await screen.findByText(/nothing to compare/)).toBeVisible()
  })

  it('file flow: previews a CSV, confirms replacing a repeated file and shows upload progress', async () => {
    mockApi()
    filePreview.mockResolvedValue({ ...preview, source: 'ga4', file_name: 'ga4.csv', overlap: { imported_days: 2, live_days: 0, same_file: { import_id: 4, imported_at: '2026-10-01T00:00:00Z' } } } as never)
    upload.mockImplementation(async (_s, _o, _src, _f, onProgress) => {
      onProgress(42)
      return new Promise(() => {})
    })
    renderWizard()
    fireEvent.click(screen.getByRole('tab', { name: 'Upload a file' }))
    fireEvent.change(screen.getByLabelText('File type'), { target: { value: 'ga4' } })
    const file = new File(['Date,Sessions\n20240301,1'], 'ga4.csv', { type: 'text/csv' })
    fireEvent.change(screen.getByLabelText('Import file'), { target: { files: [file] } })
    await toPreview()
    expect(await screen.findByText(/This exact file was imported on 2026-10-01/)).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Replace the earlier import' }))
    expect(await screen.findByText('Uploading 42%')).toBeVisible()
    expect(upload).toHaveBeenCalledWith(7, 3, 'ga4', file, expect.any(Function), undefined, true)
  })
})

describe('Google connection', () => {
  beforeEach(() => window.sessionStorage.clear())
  afterEach(() => { cleanup(); vi.clearAllMocks() })

  async function openGa4() {
    fireEvent.change(await screen.findByLabelText('Service'), { target: { value: 'ga4_api' } })
  }

  it('starts the consent flow, keeping non-secret choices for the redirect', async () => {
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    mockApi((method, path) => {
      if (path.endsWith('/ga4/connect')) return { authorization_url: 'https://accounts.example/consent' }
      if (path === '/api/private/imports/ga4/connections') return { data: [] }
      return undefined
    })
    renderWizard()
    await openGa4()
    fireEvent.click(await screen.findByRole('button', { name: 'Connect Google' }))
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://accounts.example/consent'))
    expect(window.sessionStorage.getItem('supermetry:import-draft:7')).toContain('ga4_api')
    vi.unstubAllGlobals()
  })

  it('on return with ga4=connected selects the new connection and lists properties', async () => {
    window.sessionStorage.setItem('supermetry:import-draft:7', JSON.stringify({ mode: 'api', apiMethod: 'ga4_api' }))
    mockApi((_m, path) => {
      if (path === '/api/private/imports/ga4/connections') return { data: [{ id: 5, created_at: '2026-11-18T10:00:00Z' }] }
      if (path === '/api/private/imports/ga4/connections/5/properties') return { data: [{ property: 'properties/123', display_name: 'Main site', account: 'accounts/1' }] }
      return undefined
    })
    renderWizard({ search: { ga4: 'connected' } })
    expect(await screen.findByText(/Google account connected/)).toBeVisible()
    const property = await screen.findByLabelText('Analytics property')
    fireEvent.change(property, { target: { value: 'properties/123' } })
    expect(screen.getByRole('button', { name: 'Next' })).toBeEnabled()
  })

  it.each([
    ['denied', /cancelled/],
    ['error', /did not finish/],
  ] as const)('shows the %s outcome', async (outcome, text) => {
    window.sessionStorage.setItem('supermetry:import-draft:7', JSON.stringify({ mode: 'api', apiMethod: 'ga4_api' }))
    mockApi((_m, path) => (path === '/api/private/imports/ga4/connections' ? { data: [] } : undefined))
    renderWizard({ search: { ga4: outcome } })
    expect(await screen.findByText(text)).toBeVisible()
  })
})

describe('switch-over panel', () => {
  afterEach(() => { cleanup(); vi.clearAllMocks() })

  function renderPanel() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(<QueryClientProvider client={client}><SwitchOverPanel siteId={7} orgId={3} today={new Date('2026-06-10T00:00:00Z')} /></QueryClientProvider>)
  }

  it('shows imported and collected numbers side by side with the difference', async () => {
    request.mockImplementation(async (_m, path) => {
      expect(path).toContain('from=2026-05-12&to=2026-06-10')
      return { from: '', to: '', days: [
        { day: '2026-06-08', imported: { visitors: 100, visits: 100, pageviews: 300 }, native: { visitors: 97, visits: 90, pageviews: 310 }, visitors_diff_pct: -3, pageviews_diff_pct: 3.3 },
        { day: '2026-06-07', imported: { visitors: 50, visits: 50, pageviews: 80 }, native: null, visitors_diff_pct: null, pageviews_diff_pct: null },
      ] } as never
    })
    renderPanel()
    const table = await screen.findByRole('table')
    expect(within(table).getAllByRole('row')).toHaveLength(2)
    expect(within(table).getByText('−3.0%')).toBeVisible()
    expect(within(table).getByText('+3.3%')).toBeVisible()
    expect(screen.getByText('Looks consistent')).toBeVisible()
  })

  it('explains an empty comparison', async () => {
    request.mockResolvedValue({ from: '', to: '', days: [] } as never)
    renderPanel()
    expect(await screen.findByText(/No day in this range has both imported and collected data/)).toBeVisible()
  })
})
