import '@testing-library/jest-dom/vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthorizationProvider, hasCapability } from './authorization'
import { visibleNavigation, organizationHref } from '@/components/Sidebar'

const {
  useProfiles,
  useProfile,
  useSession,
  useReplays,
  useSessions,
  useRetentionReport,
} = vi.hoisted(() => ({
  useProfiles: vi.fn(),
  useProfile: vi.fn(),
  useSession: vi.fn(),
  useReplays: vi.fn(),
  useSessions: vi.fn(),
  useRetentionReport: vi.fn(),
}))
vi.mock('@/features/profiles/api', () => ({
  useProfiles,
  useProfile,
  useDeleteProfile: vi.fn(),
}))
vi.mock('@/features/sessions/api', () => ({ useSession, useSessions }))
vi.mock('@/features/retention/api', () => ({ useRetentionReport }))
vi.mock('@/components/replay/api', () => ({ useReplays }))
import { ProfilesPage } from '@/features/profiles/ProfilesPage'
import { ProfileDetailPage } from '@/features/profiles/ProfileDetailPage'
import { ReplaysPage } from '@/components/replay/ReplaysPage'
import { ReplayPlayerPage } from '@/components/replay/ReplayPlayerPage'
import { ReplayOverlay } from '@/components/replay/ReplayOverlay'
import { ReplayLink } from '@/components/replay/ReplayLink'
import { DashboardExportMenu } from '@/components/DashboardExportMenu'
import { RetentionPage } from '@/features/retention/RetentionPage'
import { SessionsPage } from '@/features/sessions/SessionsPage'

const pager = {
  page: 1,
  onFirst: vi.fn(),
  onNext: vi.fn(),
  onPrevious: vi.fn(),
}
const range = { from: '2026-10-01', to: '2026-10-09' }
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('server capability UI boundaries', () => {
  it('fails closed on missing, empty, or unknown effective capabilities', () => {
    expect(hasCapability(undefined, 'profiles.read')).toBe(false)
    expect(hasCapability({ effective_capabilities: [] }, 'replay.read')).toBe(
      false,
    )
    expect(
      hasCapability(
        { effective_capabilities: ['admin', '*', 'profiles:read'] },
        'profiles.read',
      ),
    ).toBe(false)
    expect(
      hasCapability(
        { effective_capabilities: ['profiles.read'] },
        'profiles.read',
      ),
    ).toBe(true)
  })

  it('does not fetch or show profiles, replay content, links, overlays, or exports for billing-only access', () => {
    render(
      <AuthorizationProvider
        membership={{ effective_capabilities: ['billing.manage'] }}
      >
        <ProfilesPage
          {...pager}
          orgId={1}
          orgSlug="acme"
          siteSlug="docs"
          siteId={7}
          search=""
          onSearchChange={vi.fn()}
        />
        <ProfileDetailPage
          {...pager}
          orgId={1}
          siteId={7}
          profileId={1}
          role="viewer"
          onDeleted={vi.fn()}
        />
        <ReplaysPage
          {...pager}
          siteId={7}
          org="acme"
          site="docs"
          from={range.from}
          to={range.to}
        />
        <ReplayPlayerPage siteId={7} sessionId="123" />
        <ReplayOverlay
          open
          org="acme"
          site="docs"
          siteId={7}
          rows={[]}
          sessionId="123"
          onOpenChange={vi.fn()}
          onSessionChange={vi.fn()}
        />
        <ReplayLink hasReplay org="acme" site="docs" sessionId="123" />
        <DashboardExportMenu
          query={{
            siteId: 7,
            dateRange: range,
            filters: [],
            metrics: ['visitors'],
          }}
        />
      </AuthorizationProvider>,
    )
    expect(useProfiles).not.toHaveBeenCalled()
    expect(useProfile).not.toHaveBeenCalled()
    expect(useReplays).not.toHaveBeenCalled()
    expect(useSession).not.toHaveBeenCalled()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('keeps billing-only organization navigation reachable with no project links', () => {
    const membership = {
      org: { id: 1, slug: 'acme', name: 'Acme' },
      role: 'viewer' as const,
      sites: [],
      site_ids: [],
      billing_admin: true,
      effective_capabilities: ['billing.manage'],
    }
    const navigation = visibleNavigation(membership.role, false, membership)
    expect(navigation.every((item) => item.scope === 'org')).toBe(true)
    expect(navigation.map((item) => item.label)).not.toContain(
      'Organization settings',
    )
    expect(organizationHref(membership)).toBe('/app/acme/settings')
    expect(
      visibleNavigation('owner', true, { effective_capabilities: [] }).some(
        (item) => item.suffix === '/profiles',
      ),
    ).toBe(false)
    expect(
      visibleNavigation('viewer', true, {
        effective_capabilities: ['profiles.read'],
      }, { id: 7, name: 'Docs', tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: [] } }).some((item) => item.suffix === '/profiles'),
    ).toBe(true)
  })

  it('ignores a profile-retention URL without profile permission', () => {
    useRetentionReport.mockReturnValue({ isPending: true })
    render(
      <AuthorizationProvider membership={{ effective_capabilities: [] }}>
        <RetentionPage
          siteId={7}
          dateRange={range}
          filters={[]}
          interval="day"
          identity="profile"
          profilesEnabled
          onIntervalChange={vi.fn()}
          onIdentityChange={vi.fn()}
        />
      </AuthorizationProvider>,
    )
    expect(useRetentionReport).toHaveBeenCalledWith(
      7,
      range,
      [],
      'day',
      'anonymous',
    )
    expect(
      screen.queryByRole('option', { name: 'Identity profile' }),
    ).not.toBeInTheDocument()
  })

  it('ignores replay-only filtering and hides cached profile identity for a stats-only viewer', () => {
    useSession.mockReturnValue({})
    useSessions.mockReturnValue({
      data: {
        data: [
          {
            id: '123',
            started_at: '2026-10-09T00:00:00Z',
            duration_ms: 1000,
            pageviews: 1,
            events: 0,
            external_id: 'private-identity',
            has_replay: true,
          },
        ],
        next_cursor: null,
      },
      isPending: false,
    })
    render(
      <AuthorizationProvider
        membership={{ effective_capabilities: ['stats.read'] }}
      >
        <SessionsPage
          {...pager}
          siteId={7}
          dateRange={range}
          filters={[]}
          hasReplayOnly
          org="acme"
          site="docs"
          replayId="123"
          onSelect={vi.fn()}
          onReplayChange={vi.fn()}
        />
      </AuthorizationProvider>,
    )
    expect(useSessions).toHaveBeenCalledWith(7, range, [], undefined, false)
    expect(screen.queryByText('private-identity')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Play replay' }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('columnheader', { name: 'Replay' }),
    ).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})
