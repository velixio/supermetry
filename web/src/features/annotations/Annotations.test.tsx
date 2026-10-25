import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { del, get, patch, post } = vi.hoisted(() => ({
  del: vi.fn(),
  get: vi.fn(),
  patch: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/lib/api', () => ({ api: { DELETE: del, GET: get, PATCH: patch, POST: post } }))

import { AnnotationManager } from './AnnotationManager'

function renderManager(canEdit = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AnnotationManager
        siteId={7}
        dateRange={{ from: '2026-10-01', to: '2026-10-07' }}
        canEdit={canEdit}
      >
        {({ addButton, annotations, onEdit, onDelete }) => (
          <div>
            {addButton}
            {annotations.map((annotation) => (
              <article key={annotation.id}>
                {annotation.text}
                <button onClick={() => onEdit(annotation)}>Edit existing</button>
                <button onClick={() => onDelete(annotation)}>Delete existing</button>
              </article>
            ))}
          </div>
        )}
      </AnnotationManager>
    </QueryClientProvider>,
  )
}

describe('AnnotationManager', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    get.mockResolvedValue({ data: [], response: { ok: true, status: 200 } })
    post.mockResolvedValue({
      data: { id: 9, site_id: 7, day: '2026-10-03', text: 'Release shipped', created_at: '2026-10-03T12:00:00Z' },
      response: { ok: true, status: 201 },
    })
    patch.mockResolvedValue({
      data: { id: 8, site_id: 7, day: '2026-10-02', text: 'Updated note', created_at: '2026-10-02T12:00:00Z' },
      response: { ok: true, status: 200 },
    })
    del.mockResolvedValue({ response: { ok: true, status: 204 } })
  })

  afterEach(cleanup)

  it('adds a valid annotation and refreshes the date-range query', async () => {
    get
      .mockResolvedValueOnce({ data: [], response: { ok: true, status: 200 } })
      .mockResolvedValue({
        data: [{ id: 9, site_id: 7, day: '2026-10-03', text: 'Release shipped', created_at: '2026-10-03T12:00:00Z' }],
        response: { ok: true, status: 200 },
      })
    renderManager()

    fireEvent.click(screen.getByRole('button', { name: 'Add annotation' }))
    fireEvent.change(screen.getByLabelText('Day'), { target: { value: '2026-10-03' } })
    fireEvent.change(screen.getByLabelText('Annotation'), { target: { value: ' Release shipped ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save annotation' }))

    await waitFor(() => expect(post).toHaveBeenCalled())
    expect(post).toHaveBeenCalledWith(
      '/api/private/sites/{site}/annotations',
      expect.objectContaining({ body: { day: '2026-10-03', text: 'Release shipped' } }),
    )
    expect(await screen.findByText('Release shipped')).toBeVisible()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('validates annotation text before sending it', async () => {
    renderManager()

    fireEvent.click(screen.getByRole('button', { name: 'Add annotation' }))
    fireEvent.change(screen.getByLabelText('Annotation'), { target: { value: '   ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save annotation' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('between 1 and 200 characters')
    expect(post).not.toHaveBeenCalled()
  })

  it('updates and deletes existing annotations', async () => {
    get.mockResolvedValue({
      data: [{ id: 8, site_id: 7, day: '2026-10-02', text: 'Original note', created_at: '2026-10-02T12:00:00Z' }],
      response: { ok: true, status: 200 },
    })
    renderManager()

    await screen.findByText('Original note')
    fireEvent.click(screen.getByRole('button', { name: 'Edit existing' }))
    fireEvent.change(screen.getByLabelText('Annotation'), { target: { value: ' Updated note ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save annotation' }))
    await waitFor(() => expect(patch).toHaveBeenCalledWith(
      '/api/private/annotations/{id}',
      expect.objectContaining({ body: { day: '2026-10-02', text: 'Updated note' } }),
    ))

    fireEvent.click(screen.getByRole('button', { name: 'Delete existing' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith(
      '/api/private/annotations/{id}',
      { params: { path: { id: 8 } } },
    ))
  })

  it('does not offer editing controls to viewers', async () => {
    renderManager(false)
    await waitFor(() => expect(get).toHaveBeenCalled())
    expect(screen.queryByRole('button', { name: 'Add annotation' })).not.toBeInTheDocument()
  })
})
