import '@testing-library/jest-dom/vitest'

import { cleanup, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { renderWithRouter } from '@/test/renderWithRouter'
import { OrganizationSettingsTabs } from './OrganizationSettings'
import { AccountSettingsNavigation } from '@/routes/app.account'

afterEach(cleanup)

it('uses route-link navigation semantics for organization settings', async () => {
  renderWithRouter(<OrganizationSettingsTabs org="acme" current="members" />)

  const navigation = await screen.findByRole('navigation', { name: 'Organization settings' })
  expect(navigation).toBeVisible()
  expect(screen.getByRole('link', { name: 'Members' })).toHaveAttribute('aria-current', 'page')
  expect(screen.getByRole('link', { name: 'Overview' })).not.toHaveAttribute('aria-current')
  expect(screen.queryByRole('tablist')).not.toBeInTheDocument()
  expect(screen.queryByRole('tab')).not.toBeInTheDocument()
})

it('uses current-page route links with touch-sized account settings targets', async () => {
  renderWithRouter(<AccountSettingsNavigation />, '/app/account/security')

  const navigation = await screen.findByRole('navigation', { name: 'Account settings' })
  expect(navigation).toBeVisible()
  expect(screen.getByRole('link', { name: 'Security' })).toHaveAttribute('aria-current', 'page')
  expect(screen.getByRole('link', { name: 'Profile' })).not.toHaveAttribute('aria-current')
  for (const link of screen.getAllByRole('link')) expect(link).toHaveClass('min-h-11')
  expect(screen.queryByRole('tablist')).not.toBeInTheDocument()
  expect(screen.queryByRole('tab')).not.toBeInTheDocument()
})
