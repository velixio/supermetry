import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Dashboard } from '../api'

const mocks = vi.hoisted(() => ({
  useDashboards: vi.fn(),
  save: vi.fn(),
  create: vi.fn(),
  remove: vi.fn(),
  duplicate: vi.fn(),
  setDefault: vi.fn(),
}))

vi.mock('@tanstack/react-router', () => ({ useBlocker: () => ({ status: 'idle', proceed: vi.fn(), reset: vi.fn() }) }))
vi.mock('../api', () => ({ useDashboards: mocks.useDashboards, dashboardKeys: { list: (id: number) => ['dashboards', id] } }))
vi.mock('../widgets', () => ({
  widgetTitle: (widget: { id: string; kind: string }) => widget.id,
  WidgetBoundary: ({ children }: { children: ReactNode }) => <>{children}</>,
  WidgetBody: ({ widget }: { widget: { id: string } }) => <p>preview {widget.id}</p>,
}))
vi.mock('@/features/funnels/api', () => ({ useFunnels: () => ({ data: [{ id: 5, name: 'Signup' }] }) }))
vi.mock('@/components/AddFilterPicker', () => ({ AddFilterPicker: () => <button type="button">Add filter</button> }))
vi.mock('./editorApi', async (original) => {
  const actual = await original<typeof import('./editorApi')>()
  const hook = (fn: ReturnType<typeof vi.fn>) => () => ({ mutateAsync: fn, isPending: false })
  return {
    ...actual,
    useSaveDashboard: hook(mocks.save),
    useCreateDashboard: hook(mocks.create),
    useDeleteDashboard: hook(mocks.remove),
    useDuplicateDashboard: hook(mocks.duplicate),
    useSetDefaultDashboard: hook(mocks.setDefault),
  }
})

import { DashboardEditor, type DashboardEditorProps } from './DashboardEditor'
import { conflictKind } from './editorApi'

const dashboard: Dashboard = {
  id: 9, site_id: 3, name: 'Marketing', is_default: false, builtin: false, version: 4,
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
  layout: {
    schema_version: 1,
    period: '7d',
    widgets: [
      { id: 'one', kind: 'goals', x: 0, y: 0, w: 6, h: 4, config: { limit: 5 } },
      { id: 'two', kind: 'retention', x: 6, y: 0, w: 6, h: 4 },
    ],
  },
}

const props = (extra: Partial<DashboardEditorProps> = {}): DashboardEditorProps => ({
  mode: { kind: 'edit', id: 9 },
  siteId: 3,
  orgId: 1,
  canWrite: true,
  anonymous: false,
  base: { siteId: 3, dateRange: { from: '2026-01-01', to: '2026-01-02' }, metrics: ['visitors'], filters: [] },
  timezone: 'UTC',
  onOpen: vi.fn(),
  onExit: vi.fn(),
  onDeleted: vi.fn(),
  ...extra,
})

const ids = () => screen.getAllByText(/^preview /).map((node) => node.textContent)

describe('DashboardEditor', () => {
  beforeEach(() => {
    mocks.useDashboards.mockReturnValue({ isPending: false, isError: false, data: [dashboard], refetch: vi.fn().mockResolvedValue({ data: [{ ...dashboard, version: 5, name: 'Renamed' }] }) })
  })
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('shows no editor to a viewer', () => {
    render(<DashboardEditor {...props({ canWrite: false })} />)
    expect(screen.getByRole('status')).toHaveTextContent('only owners, admins and editors')
    expect(screen.queryByRole('button', { name: /Save/ })).toBeNull()
  })

  it('moves and resizes with buttons and Alt+arrows and announces each change', () => {
    render(<DashboardEditor {...props()} />)
    expect(ids()).toEqual(['preview one', 'preview two'])
    fireEvent.click(screen.getByRole('button', { name: 'Move later: one' }))
    expect(ids()).toEqual(['preview two', 'preview one'])
    expect(screen.getAllByRole('status').some((node) => node.textContent === 'one moved to position 2 of 2.')).toBe(true)
    fireEvent.keyDown(screen.getByRole('button', { name: 'Make wider: two' }), { key: 'ArrowRight', altKey: true })
    expect(screen.getAllByRole('status').some((node) => node.textContent === 'two is now 7 of 12 columns wide.')).toBe(true)
    expect(screen.getByText(/Unsaved changes/)).toBeInTheDocument()
  })

  it('saves with the loaded version and then reports a stale version as a conflict', async () => {
    mocks.save.mockRejectedValueOnce({ status: 409, detail: 'the dashboard changed since it was loaded; reload and retry' })
    render(<DashboardEditor {...props()} />)
    fireEvent.click(screen.getByRole('button', { name: 'Remove: one' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('This dashboard changed elsewhere'))
    expect(mocks.save).toHaveBeenCalledWith(expect.objectContaining({ id: 9, version: 4, name: 'Marketing' }))
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
    // Reload replaces the draft with the latest server version and re-enables editing.
    fireEvent.click(screen.getByRole('button', { name: 'Reload latest version' }))
    await waitFor(() => expect(screen.getByDisplayValue('Renamed')).toBeInTheDocument())
    expect(ids()).toEqual(['preview one', 'preview two'])
    expect(screen.queryByText('This dashboard changed elsewhere')).toBeNull()
  })

  it('refuses to save a blank name and sends nothing', async () => {
    render(<DashboardEditor {...props()} />)
    fireEvent.change(screen.getByLabelText('Dashboard name'), { target: { value: '  ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(await screen.findByText('Enter a name of 1 to 80 characters.', { selector: 'li' })).toBeInTheDocument()
    expect(mocks.save).not.toHaveBeenCalled()
  })

  it('maps a taken name to the name field and a 400 to the server messages', async () => {
    mocks.save.mockRejectedValueOnce({ status: 409, detail: 'a dashboard with this name already exists' })
    render(<DashboardEditor {...props()} />)
    fireEvent.change(screen.getByLabelText('Dashboard name'), { target: { value: 'Other' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(await screen.findByText(/Another dashboard already uses this name/)).toBeInTheDocument()
    mocks.save.mockRejectedValueOnce({ status: 400, detail: 'invalid dashboard', errors: [{ field: 'layout.widgets[0]', message: 'metric not allowed' }] })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(await screen.findByText('layout.widgets[0]: metric not allowed')).toBeInTheDocument()
  })

  it('creates a new dashboard from a template and hands it to the route', async () => {
    const created = { ...dashboard, id: 12, name: 'New dashboard' }
    mocks.create.mockResolvedValueOnce(created)
    const onOpen = vi.fn()
    render(<DashboardEditor {...props({ mode: { kind: 'new', template: 'marketing' }, onOpen })} />)
    expect(screen.getByRole('heading', { name: 'New dashboard' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Create dashboard' }))
    await waitFor(() => expect(onOpen).toHaveBeenCalledWith(created))
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ name: 'New dashboard' }))
  })

  it('asks before deleting and reports the result', async () => {
    mocks.remove.mockResolvedValueOnce(undefined)
    const onDeleted = vi.fn()
    render(<DashboardEditor {...props({ onDeleted })} />)
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(mocks.remove).not.toHaveBeenCalled()
    fireEvent.click(await screen.findByRole('button', { name: 'Delete dashboard' }))
    await waitFor(() => expect(onDeleted).toHaveBeenCalled())
    expect(mocks.remove).toHaveBeenCalledWith(9)
  })
})

describe('conflictKind', () => {
  it('tells a stale version from a taken name and the dashboard limit', () => {
    const problem = (detail: string) => ({ type: 't', title: 'Conflict', status: 409, detail })
    expect(conflictKind(problem('the dashboard changed since it was loaded; reload and retry'))).toBe('stale')
    expect(conflictKind(problem('a dashboard with this name already exists'))).toBe('name')
    expect(conflictKind(problem('a site can keep at most 50 dashboards'))).toBe('limit')
    expect(conflictKind({ type: 't', title: 'Bad', status: 400 })).toBeUndefined()
  })
})
