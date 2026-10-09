import '@testing-library/jest-dom/vitest'
import { useEffect, type ReactNode } from 'react'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({ Link: ({ children }: { children: ReactNode }) => <a>{children}</a> }))

import type { Site } from '@/routes/app.$org'
import { AnalyticsPrivacyBoundary, anonymousRouteRestricted } from './AnalyticsPrivacyBoundary'

const anonymous: Site = { id: 1, name: 'Docs', profiles_enabled: true, tracking_policy: { instance_mode: 'anonymous', effective_mode: 'anonymous', configured_profiles_enabled: true, restrictions: ['instance_anonymous'] } }
const identified: Site = { ...anonymous, profiles_enabled: false, tracking_policy: { instance_mode: 'identified', effective_mode: 'anonymous', configured_profiles_enabled: false, restrictions: ['project_profiles_disabled'] } }

afterEach(cleanup)

describe('analytics route privacy boundary', () => {
  it.each(['events', '%65vents', 'EVENTS', 'Replays/123', 'settings/IMPORTS', '%73ettings/%69mports', 'sessions', 'profiles', 'profiles/123', 'replays', 'replays/123', 'journeys', 'heatmaps', 'funnels', 'funnels/12', 'vitals', 'retention', 'goals', 'realtime', 'settings/imports', 'settings/goals', 'settings/funnels'])('prevents child hooks from mounting for anonymous %s', (suffix) => {
    const query = vi.fn()
    function RestrictedPage() { useEffect(query, []); return <p>Detailed records</p> }
    render(<AnalyticsPrivacyBoundary site={anonymous} org="acme" requestedSite="docs" pathname={`/app/acme/docs/${suffix}`}><RestrictedPage /></AnalyticsPrivacyBoundary>)
    expect(screen.getByRole('status', { name: 'Tracking privacy restriction' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Unavailable in anonymous tracking mode' })).toBeVisible()
    expect(screen.queryByText('Detailed records')).not.toBeInTheDocument()
    expect(query).not.toHaveBeenCalled()
  })

  it.each(['/app/$org/$site/events', '/app/acme/docs/events'])('keeps a previous rendered route restricted while the target dashboard loads: %s', (previous) => {
    const query = vi.fn()
    function PreviousPage() { useEffect(query, []); return <p>Old detailed page</p> }
    render(<AnalyticsPrivacyBoundary site={anonymous} org="acme" requestedSite="docs" pathname="/app/acme/docs" renderedPathnames={[previous]}><PreviousPage /></AnalyticsPrivacyBoundary>)
    expect(screen.getByRole('heading', { name: 'Unavailable in anonymous tracking mode' })).toBeVisible()
    expect(query).not.toHaveBeenCalled()
  })

  it('uses instance mode, permitting identified routes with project profiles off', () => {
    const query = vi.fn()
    function Page() { useEffect(query, []); return <p>Detailed records</p> }
    render(<AnalyticsPrivacyBoundary site={identified} org="acme" requestedSite="docs" pathname="/app/acme/docs/events"><Page /></AnalyticsPrivacyBoundary>)
    expect(screen.getByText('Detailed records')).toBeVisible()
    expect(query).toHaveBeenCalledOnce()
  })

  it('fails closed with a policy explanation while policy is unknown', () => {
    render(<AnalyticsPrivacyBoundary site={{ id: 1, name: 'Docs' }} org="acme" requestedSite="docs" pathname="/app/acme/docs/events"><p>Records</p></AnalyticsPrivacyBoundary>)
    expect(screen.getByRole('heading', { name: 'Tracking policy is unavailable' })).toBeVisible()
    expect(screen.queryByText('Records')).not.toBeInTheDocument()
  })

  it.each(['/app/acme/docs', '/app/acme/docs/settings/general', '/app/acme/docs/settings/tracking', '/app/acme/docs/settings/replay', '/app/acme/docs/settings/installation', '/app/acme/settings/members'])('preserves supported route %s', (pathname) => {
    expect(anonymousRouteRestricted(pathname)).toBe(false)
    render(<AnalyticsPrivacyBoundary site={anonymous} org="acme" requestedSite="docs" pathname={pathname}><p>Supported content</p></AnalyticsPrivacyBoundary>)
    expect(screen.getByText('Supported content')).toBeVisible()
  })
})
