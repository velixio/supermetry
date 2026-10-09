import { afterEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('./api', () => ({ api: { POST: post } }))
import { downloadStatsCSV } from './csv'

const query = { siteId: 1, dateRange: { from: '2026-10-01', to: '2026-10-02' }, dimensions: ['page'], metrics: ['visitors' as const], filters: [] }
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('CSV coverage labels', () => {
  it('preserves the server verified-only filename for the downloaded CSV', async () => {
    const names: string[] = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { names.push(this.download) })
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:test') })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() })
    post.mockResolvedValue({ data: 'page,visitors\n/public,1', response: new Response(null, { headers: { 'Content-Disposition': 'attachment; filename="site-query-verified-only.csv"', 'X-Supermetry-Privacy-Scope': 'verified-only' } }) })
    await downloadStatsCSV(query)
    expect(names).toEqual(['site-query-verified-only.csv'])
  })

  it('preserves genuine authorization failures rather than downloading an empty file', async () => {
    const error = { status: 403, detail: 'Policy restricted' }
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    post.mockResolvedValue({ error, response: new Response(null, { status: 403 }) })
    await expect(downloadStatsCSV(query)).rejects.toEqual(error)
    expect(click).not.toHaveBeenCalled()
  })
})
