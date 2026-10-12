import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { Dashboard } from './api'

const { useDashboards } = vi.hoisted(() => ({ useDashboards: vi.fn() }))
vi.mock('./api', () => ({ useDashboards }))
vi.mock('./widgets', () => ({
  widgetTitle: (widget: { title?: string; id: string }) => widget.title ?? widget.id,
  WidgetBoundary: ({ children }: { children: ReactNode }) => <>{children}</>,
  WidgetBody: ({ widget, query }: { widget: { id: string }; query: { dateRange: { from: string; to: string } } }) => (
    <p data-testid={`body-${widget.id}`}>{query.dateRange.from}/{query.dateRange.to}</p>
  ),
}))

import { DashboardViewer, type DashboardViewerProps } from './DashboardViewer'

const dashboard: Dashboard = {
  id: 9, site_id: 3, name: 'Marketing', is_default: false, builtin: false, version: 1,
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
  layout: {
    schema_version: 1,
    period: '7d',
    widgets: [
      { id: 'late', kind: 'goals', x: 0, y: 4, w: 12, h: 3 },
      { id: 'early', kind: 'map', x: 0, y: 0, w: 4, h: 3, period: 'today' },
    ],
  },
}

const props = (extra: Partial<DashboardViewerProps> = {}): DashboardViewerProps => ({
  dashboardId: 9,
  base: { siteId: 3, dateRange: { from: '2026-01-01', to: '2026-01-02' }, metrics: ['visitors'], filters: [] },
  timezone: 'UTC',
  urlFilters: [],
  compare: 'off',
  anonymous: false,
  onFilter: vi.fn(),
  onShorten: vi.fn(),
  onOpenStandard: vi.fn(),
  ...extra,
})

describe('DashboardViewer', () => {
  afterEach(cleanup)

  it('renders widgets in reading order with their column spans', () => {
    useDashboards.mockReturnValue({ data: [dashboard], isPending: false, isError: false })
    const { container } = render(<DashboardViewer {...props()} />)
    const ids = [...container.querySelectorAll('[data-widget-id]')].map((node) => node.getAttribute('data-widget-id'))
    expect(ids).toEqual(['early', 'late'])
    expect(container.querySelector('[data-widget-id="late"]')).toHaveStyle('--span: 12')
    expect(container.querySelector('[data-widget-id="early"]')).toHaveStyle('--wide: 1')
  })

  it('gives each widget its own range: an override beats the URL period', () => {
    useDashboards.mockReturnValue({ data: [dashboard], isPending: false, isError: false })
    render(<DashboardViewer {...props({ period: '28d' })} />)
    const range = (id: string) => {
      const [from, to] = (screen.getByTestId(`body-${id}`).textContent ?? '').split('/')
      return (Date.parse(to) - Date.parse(from)) / 86_400_000 + 1
    }
    expect(range('late')).toBe(28)
    expect(range('early')).toBe(1)
  })

  it('explains a missing dashboard and offers the standard one', () => {
    useDashboards.mockReturnValue({ data: [], isPending: false, isError: false })
    render(<DashboardViewer {...props({ dashboardId: 77 })} />)
    expect(screen.getByRole('heading', { name: 'This dashboard no longer exists' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Open the standard dashboard' })).toBeVisible()
  })

  it('shows an empty state for a layout with no widgets and an error with retry', () => {
    useDashboards.mockReturnValue({ data: [{ ...dashboard, layout: { schema_version: 1, widgets: [] } }], isPending: false, isError: false })
    const { unmount } = render(<DashboardViewer {...props()} />)
    expect(screen.getByRole('heading', { name: 'This dashboard has no widgets yet' })).toBeVisible()
    unmount()
    useDashboards.mockReturnValue({ isPending: false, isError: true, error: { detail: 'nope' }, refetch: vi.fn() })
    render(<DashboardViewer {...props()} />)
    expect(screen.getByRole('alert')).toHaveTextContent('nope')
  })
})
