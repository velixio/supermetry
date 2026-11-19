import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { Dashboard } from './api'

const { useDashboards } = vi.hoisted(() => ({ useDashboards: vi.fn() }))
vi.mock('./api', () => ({ useDashboards }))

import { DashboardSwitcher } from './DashboardSwitcher'

const saved = (id: number, name: string, extra: Partial<Dashboard> = {}): Dashboard => ({
  id, site_id: 3, name, is_default: false, builtin: false, version: 1,
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
  layout: { schema_version: 1, widgets: [] }, ...extra,
})

const open = () => fireEvent.pointerDown(screen.getByRole('button', { name: /Switch dashboard/ }), { button: 0, ctrlKey: false, pointerType: 'mouse' })

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('DashboardSwitcher', () => {
  it('lists the standard and saved dashboards and ends with New dashboard for editors', () => {
    useDashboards.mockReturnValue({ data: [saved(9, 'Marketing')] })
    const onNew = vi.fn()
    const onChange = vi.fn()
    render(<DashboardSwitcher siteId={3} value="builtin" onChange={onChange} onDefault={vi.fn()} canWrite onNew={onNew} />)

    expect(screen.getByRole('button', { name: 'Switch dashboard: Standard dashboard' })).toBeVisible()
    open()
    expect(screen.getAllByRole('menuitemradio').map((item) => item.textContent)).toEqual(['Standard dashboard', 'Marketing'])
    const items = screen.getAllByRole('menuitem')
    expect(items.at(-1)).toHaveTextContent('New dashboard')
    fireEvent.click(items.at(-1)!)
    expect(onNew).toHaveBeenCalledOnce()
  })

  it('switches to a saved dashboard', () => {
    const marketing = saved(9, 'Marketing')
    useDashboards.mockReturnValue({ data: [marketing] })
    const onChange = vi.fn()
    render(<DashboardSwitcher siteId={3} value="builtin" onChange={onChange} onDefault={vi.fn()} canWrite onNew={vi.fn()} />)
    open()
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Marketing' }))
    expect(onChange).toHaveBeenCalledWith('9', marketing)
  })

  it('offers no New dashboard to viewers', () => {
    useDashboards.mockReturnValue({ data: [saved(9, 'Marketing')] })
    render(<DashboardSwitcher siteId={3} value="builtin" onChange={vi.fn()} onDefault={vi.fn()} canWrite={false} onNew={vi.fn()} />)
    open()
    expect(screen.queryByRole('menuitem', { name: 'New dashboard' })).not.toBeInTheDocument()
  })

  it('is hidden for a viewer when there is nothing to switch to, but shown to an editor', () => {
    useDashboards.mockReturnValue({ data: [] })
    const { container } = render(<DashboardSwitcher siteId={3} value={undefined} onChange={vi.fn()} onDefault={vi.fn()} canWrite={false} />)
    expect(container).toBeEmptyDOMElement()
    cleanup()
    render(<DashboardSwitcher siteId={3} value={undefined} onChange={vi.fn()} onDefault={vi.fn()} canWrite onNew={vi.fn()} />)
    expect(screen.getByRole('button', { name: /Switch dashboard/ })).toBeVisible()
  })
})
