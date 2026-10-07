import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import {
  InstallGuide,
  SiteSettingsTabs,
  installationStatusInterval,
} from './SiteSettings'
import { renderWithRouter } from '@/test/renderWithRouter'

describe('site settings', () => {
  afterEach(cleanup)

  it('shows all settings tabs with Installation in place of Tracking', async () => {
    renderWithRouter(<SiteSettingsTabs org="acme" site="docs" current="installation" />)
    for (const label of ['General', 'Installation', 'Goals', 'Funnels', 'Shares', 'Reports']) {
      expect(await screen.findByRole('link', { name: label })).toBeVisible()
    }
    expect(screen.getByRole('link', { name: 'Installation' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    expect(screen.queryByText('Tracking')).not.toBeInTheDocument()
  })

  it('shows install guides, proxy advice, and a relative live status', () => {
    render(
      <InstallGuide
        publicId="site-public"
        lastEventReceivedAt="2026-10-07T09:59:40Z"
        now={new Date('2026-10-07T10:00:00Z')}
      />,
    )

    expect(screen.getByText('✓ Last event 20 seconds ago')).toBeVisible()
    expect(screen.getByText(/proxy the script through your own domain/i)).toBeVisible()
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Next.js / React' }), {
      button: 0,
      ctrlKey: false,
    })
    expect(screen.getByText(/root layout/i)).toBeVisible()
    fireEvent.mouseDown(screen.getByRole('tab', { name: 'WordPress / other CMS' }), {
      button: 0,
      ctrlKey: false,
    })
    expect(screen.getByText(/header injection/i)).toBeVisible()
  })

  it('polls every five seconds until the first event', () => {
    expect(installationStatusInterval(undefined)).toBe(5_000)
    expect(installationStatusInterval('2026-10-07T10:00:00Z')).toBe(false)
  })
})
