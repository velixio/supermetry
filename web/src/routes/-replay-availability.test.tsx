import '@testing-library/jest-dom/vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { availability, me } = vi.hoisted(() => ({ availability: vi.fn(), me: vi.fn() }))
vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: { component: () => React.ReactNode }) => ({
    options,
    useParams: () => ({ org: 'acme', site: 'example', id: '123' }),
    useSearch: () => ({ period: '7d', compare: 'off', page: 1 }),
    useNavigate: () => vi.fn(),
  }),
}))
vi.mock('./$org', () => ({ useMe: me }))
vi.mock('@/components/replay/api', () => ({ useReplayAvailability: availability }))
vi.mock('@/components/replay/ReplaysPage', () => ({ ReplaysPage: () => <div>Historical replay list</div> }))
vi.mock('@/components/replay/ReplayPlayerPage', () => ({ ReplayPlayerPage: () => <div>Historical replay player</div> }))
vi.mock('@/components/DatePicker', () => ({ DatePicker: () => null }))
import { Route as ListRoute } from './$org.$site.replays.index'
import { Route as DetailRoute } from './$org.$site.replays.$id'

const routes = [
  ['list', ListRoute.options.component!, 'Historical replay list'],
  ['detail', DetailRoute.options.component!, 'Historical replay player'],
] as const

afterEach(cleanup)
beforeEach(() => {
  me.mockReturnValue({ data: { memberships: [{ org: { slug: 'acme' }, role: 'owner', effective_capabilities: ['replay.read'], sites: [{ id: 7, slug: 'example', timezone: 'UTC' }] }] } })
  availability.mockReturnValue({ enabled: false, isPending: false, isError: false, data: { enabled: false, instance_enabled: true, configured_enabled: false, restrictions: ['project_replay_disabled'] }, refetch: vi.fn() })
})

describe.each(routes)('replay %s route', (_name, Component, history) => {
  it('retains historical access with an explicit project recording-off notice', () => {
    render(<Component />)
    expect(screen.getByText(history)).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Recording is off' })).toBeVisible()
    expect(screen.getByText(/This project has not opted in/)).toBeVisible()
    expect(screen.getByRole('link', { name: 'Replay settings' })).toHaveAttribute('href', '/acme/example/settings/replay')
  })
  it('explains instance-disabled capture without hiding history', () => {
    availability.mockReturnValue({ enabled: false, isPending: false, data: { enabled: false, instance_enabled: false, configured_enabled: true, restrictions: ['instance_replay_disabled'] } })
    render(<Component />)
    expect(screen.getByText(history)).toBeVisible()
    expect(screen.getByText(/The instance does not permit new recording/)).toBeVisible()
  })
  it('shows effective enabled state', () => {
    availability.mockReturnValue({ enabled: true, isPending: false, data: { enabled: true, instance_enabled: true, configured_enabled: true, restrictions: [] } })
    render(<Component />)
    expect(screen.getByText(history)).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Recording is on' })).toBeVisible()
  })
  it('shows loading and error states instead of a blank route', () => {
    availability.mockReturnValue({ enabled: false, isPending: true, data: undefined })
    const view = render(<Component />)
    expect(screen.getByText('Loading recording settings…')).toBeVisible()
    availability.mockReturnValue({ enabled: false, isPending: false, isError: true, error: new Error('Settings unavailable'), refetch: vi.fn() })
    view.rerender(<Component />)
    expect(screen.getByText('Recording settings could not be loaded.')).toBeVisible()
    expect(screen.getByText('Settings unavailable')).toBeVisible()
    expect(screen.getByText(history)).toBeVisible()
  })
  it('does not mount historical data without the read grant', () => {
    me.mockReturnValue({ data: { memberships: [{ org: { slug: 'acme' }, role: 'viewer', effective_capabilities: [], sites: [{ id: 7, slug: 'example' }] }] } })
    render(<Component />)
    expect(screen.queryByText(history)).not.toBeInTheDocument()
    expect(availability).toHaveBeenCalledWith(undefined)
  })
})
