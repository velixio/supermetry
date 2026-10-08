import '@testing-library/jest-dom/vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { deleteRequest, get, patch, post } = vi.hoisted(() => ({
  deleteRequest: vi.fn(),
  get: vi.fn(),
  patch: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  api: { DELETE: deleteRequest, GET: get, PATCH: patch, POST: post },
}))

import { FilterBar } from './FilterBar'
import type { Filter } from '@/lib/search'

const filters: Filter[] = [{ dimension: 'country', operator: 'is', values: ['IN'] }]
const saved = {
  id: 3,
  site_id: 7,
  name: 'India traffic',
  filters,
}

function response(data?: unknown, status = 200) {
  return { data, response: new Response(null, { status }) }
}

function renderBar({ canEdit = true, current = [] as Filter[] } = {}) {
  const onApply = vi.fn()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <FilterBar
        canEdit={canEdit}
        siteId={7}
        filters={current}
        dropped={[]}
        invalidNotice="Invalid filters"
        filterLabel={(filter) => filter.dimension}
        removeLabel={(filter) => `Remove ${filter.dimension}`}
        onApply={onApply}
        onRemove={vi.fn()}
      />
    </QueryClientProvider>,
  )
  return { onApply }
}

async function openSegments() {
  const trigger = await screen.findByRole('button', { name: /Segments/ })
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false })
}

describe('saved segments', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockResolvedValue(response([saved]))
    post.mockResolvedValue(response(saved, 201))
    patch.mockResolvedValue(response({ ...saved, name: 'India visitors' }))
    deleteRequest.mockResolvedValue(response(undefined, 204))
  })

  afterEach(cleanup)

  it('applies a saved segment and shows its name when active', async () => {
    const { onApply } = renderBar()

    await openSegments()
    fireEvent.click(await screen.findByRole('menuitem', { name: 'India traffic' }))

    expect(onApply).toHaveBeenCalledWith(filters)
    cleanup()
    renderBar({ current: filters })
    expect(await screen.findByRole('button', { name: 'Segments: India traffic' })).toBeVisible()
  })

  it('recognizes an active segment regardless of filter and value order', async () => {
    get.mockResolvedValue(response([{
      ...saved,
      filters: [
        { dimension: 'country', operator: 'is', values: ['IN', 'US'] },
        { dimension: 'path', operator: 'contains', values: ['/docs', '/pricing'] },
      ],
    }]))

    renderBar({
      current: [
        { dimension: 'path', operator: 'contains', values: ['/pricing', '/docs'] },
        { dimension: 'country', operator: 'is', values: ['US', 'IN'] },
      ],
    })

    expect(await screen.findByRole('button', { name: 'Segments: India traffic' })).toBeVisible()
  })

  it('saves the current filters with a valid name and refetches the list', async () => {
    renderBar({ current: filters })

    await screen.findByRole('button', { name: 'Segments: India traffic' })
    await openSegments()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Save current filters as segment…' }))
    fireEvent.change(screen.getByLabelText('Segment name'), {
      target: { value: 'India traffic' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save segment' }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(
      '/api/private/sites/{site_id}/segments',
      expect.objectContaining({
        body: { name: 'India traffic', filters },
        params: { path: { site_id: 7 } },
      }),
    )
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  })

  it('renames a segment while retaining its saved filters', async () => {
    renderBar({ current: filters })

    await screen.findByRole('button', { name: 'Segments: India traffic' })
    await openSegments()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Rename India traffic' }))
    fireEvent.change(screen.getByLabelText('Segment name'), {
      target: { value: 'India visitors' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Rename segment' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/api/private/segments/{segment_id}', {
      body: { name: 'India visitors', filters },
      params: { path: { segment_id: 3 } },
    })
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  })

  it('accepts a segment name containing 80 Unicode characters', async () => {
    renderBar({ current: filters })
    const name = '😀'.repeat(80)

    await screen.findByRole('button', { name: 'Segments: India traffic' })
    await openSegments()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Save current filters as segment…' }))
    fireEvent.change(screen.getByLabelText('Segment name'), { target: { value: name } })
    fireEvent.click(screen.getByRole('button', { name: 'Save segment' }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(
      '/api/private/sites/{site_id}/segments',
      expect.objectContaining({ body: { name, filters } }),
    )
  })

  it('deletes a segment after confirmation and viewers cannot save', async () => {
    renderBar({ current: filters })

    await screen.findByRole('button', { name: 'Segments: India traffic' })
    await openSegments()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Delete India traffic' }))
    expect(screen.getByRole('alertdialog', { name: 'Delete segment?' })).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Delete segment' }))
    await waitFor(() => expect(deleteRequest).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2))

    cleanup()
    renderBar({ current: filters, canEdit: false })
    await screen.findByRole('button', { name: 'Segments: India traffic' })
    await openSegments()
    expect(
      screen.getByRole('menuitem', { name: 'Save current filters as segment…' }),
    ).toHaveAttribute('data-disabled')
    expect(screen.queryByRole('menuitem', { name: 'Delete India traffic' })).not.toBeInTheDocument()
  })

  it('keeps delete confirmation open when the API rejects deletion', async () => {
    deleteRequest.mockResolvedValue({
      error: { detail: 'The segment could not be deleted.' },
      response: new Response(null, { status: 500 }),
    })
    renderBar({ current: filters })

    await screen.findByRole('button', { name: 'Segments: India traffic' })
    await openSegments()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Delete India traffic' }))
    fireEvent.click(screen.getByRole('button', { name: 'Delete segment' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The segment could not be deleted.',
    )
    expect(screen.getByRole('alertdialog', { name: 'Delete segment?' })).toBeVisible()
  })

  it('renders loading errors as disabled menu items', async () => {
    get.mockRejectedValue(new Error('Could not load segments.'))
    renderBar()

    await openSegments()
    const error = await screen.findByRole('menuitem', { name: 'Could not load segments.' })
    expect(error).toHaveAttribute('data-disabled')
  })
})
