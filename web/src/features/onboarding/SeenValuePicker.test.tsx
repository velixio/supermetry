import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useSuggestions } = vi.hoisted(() => ({ useSuggestions: vi.fn() }))
vi.mock('@/features/events/api', () => ({ useSuggestions }))

import { FunnelBuilder } from '@/features/funnels/FunnelBuilder'
import { GoalEditor } from '@/features/goals/GoalEditor'

const source = { siteId: 7, orgId: 1, dateRange: { from: '2026-11-01', to: '2026-11-18' } }

function wrap(ui: React.ReactNode) {
  return render(<QueryClientProvider client={new QueryClient()}>{ui}</QueryClientProvider>)
}

beforeEach(() => {
  useSuggestions.mockImplementation((_site: number, _org: number, _range: unknown, dimension: string) => ({
    isPending: false,
    isError: false,
    data: dimension === 'event' ? [{ value: 'Signup', count: 12 }] : [{ value: '/pricing', count: 40 }, { value: '/docs', count: 9 }],
  }))
})
afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('goal value picker', () => {
  it('offers pages already seen and fills the value, keeping free text possible', () => {
    wrap(<GoalEditor pending={false} onSave={vi.fn()} suggest={source} />)
    expect(useSuggestions.mock.calls.at(-1)?.[3]).toBe('page')
    fireEvent.click(screen.getByRole('button', { name: '/pricing, 40 views' }))
    expect(screen.getByLabelText('Goal value')).toHaveValue('/pricing')
    expect(screen.getByLabelText('Goal name')).toHaveValue('/pricing')
    fireEvent.change(screen.getByLabelText('Goal value'), { target: { value: '/custom' } })
    expect(screen.getByLabelText('Goal value')).toHaveValue('/custom')
  })

  it('switches to event names for event goals', () => {
    wrap(<GoalEditor pending={false} onSave={vi.fn()} suggest={source} />)
    fireEvent.change(screen.getByLabelText('Goal type'), { target: { value: 'event' } })
    fireEvent.click(screen.getByRole('button', { name: 'Signup, 12 events' }))
    expect(screen.getByLabelText('Goal value')).toHaveValue('Signup')
  })

  it('shows no picker without a source', () => {
    wrap(<GoalEditor pending={false} onSave={vi.fn()} />)
    expect(screen.queryByRole('group', { name: 'Seen on your site' })).not.toBeInTheDocument()
  })
})

describe('funnel step picker', () => {
  it('fills a step from a seen page and saves it', async () => {
    const onSubmit = vi.fn()
    wrap(<FunnelBuilder suggest={source} onSubmit={onSubmit} />)
    fireEvent.click(screen.getByRole('button', { name: 'Add step' }))
    fireEvent.click(screen.getAllByRole('button', { name: '/pricing, 40 views' })[0])
    fireEvent.change(screen.getByLabelText('Step 2 type'), { target: { value: 'event' } })
    fireEvent.click(screen.getAllByRole('button', { name: 'Signup, 12 events' })[0])
    expect(screen.getByLabelText('Step 1')).toHaveValue('/pricing')
    expect(screen.getByLabelText('Step 2')).toHaveValue('Signup')
    fireEvent.click(screen.getByRole('button', { name: 'Save funnel' }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith([
      expect.objectContaining({ type: 'page', value: '/pricing' }),
      expect.objectContaining({ type: 'event', value: 'Signup' }),
    ]))
  })

  it('says so when nothing has been seen yet', () => {
    useSuggestions.mockReturnValue({ isPending: false, isError: false, data: [] })
    wrap(<FunnelBuilder suggest={source} onSubmit={vi.fn()} />)
    expect(screen.getByText('Nothing seen yet. Type a value instead.')).toBeVisible()
  })
})
