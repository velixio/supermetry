import '@testing-library/jest-dom/vitest'

import { cleanup, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { renderWithRouter } from '@/test/renderWithRouter'
import { OrganizationSettingsTabs } from './OrganizationSettings'

describe('organization settings navigation', () => {
  afterEach(cleanup)

  it('links every organization setting and marks Notifications current', async () => {
    renderWithRouter(<OrganizationSettingsTabs org="acme" current="notifications" />)
    for (const label of ['Members', 'Invitations', 'API keys', 'Audit log', 'Notifications']) {
      expect(await screen.findByRole('link', { name: label })).toBeVisible()
    }
    expect(screen.getByRole('link', { name: 'Notifications' })).toHaveAttribute('aria-current', 'page')
  })
})
