import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params }: { children: React.ReactNode; to: string; params: Record<string, string> }) => <a href={to.replace('$org', params.org).replace('$site', params.site)}>{children}</a>,
}))

import { AppEmptyState } from './AppEmptyState'
import { siteNeedsDetails } from '@/lib/me'

afterEach(cleanup)

describe('AppEmptyState', () => {
  it('invites setup and links to the installation page', () => {
    render(<AppEmptyState org="acme" site="desk" />)
    expect(screen.getByText('No reports yet')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Set up this app' })).toHaveAttribute('href', '/app/acme/desk/settings/installation')
  })
  it('treats app as a known kind that needs no extra site fetch', () => {
    expect(siteNeedsDetails('app')).toBe(false)
    expect(siteNeedsDetails('web')).toBe(false)
    expect(siteNeedsDetails('pixel')).toBe(false)
    expect(siteNeedsDetails(undefined)).toBe(true)
  })
})
