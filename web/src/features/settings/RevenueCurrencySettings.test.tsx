import '@testing-library/jest-dom/vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { RevenueCurrencySettings } from './SettingsPages'

const site = {
  id: 1,
  public_id: 'site-public',
  name: 'Store',
  allowed_hosts: ['example.test'],
  timezone: 'UTC',
  retention_days: 30,
  profiles_enabled: false,
}

describe('RevenueCurrencySettings', () => {
  afterEach(cleanup)

  it('lets an owner configure an unconfigured project with permanent-choice copy', () => {
    render(<RevenueCurrencySettings role="owner" site={{ ...site, revenue_currency_state: 'unconfigured' }} />)
    expect(screen.getByLabelText('Revenue currency')).toBeRequired()
    expect(screen.getByText(/choice is permanent/i)).toBeVisible()
  })

  it('renders the configured currency as locked', () => {
    render(<RevenueCurrencySettings role="admin" site={{ ...site, revenue_currency: 'EUR', revenue_currency_state: 'configured' }} />)
    expect(screen.getByDisplayValue('EUR')).toHaveAttribute('readonly')
    expect(screen.getByText(/permanent for the project/i)).toBeVisible()
  })

  it('explains conflict without blocking ordinary analytics', () => {
    render(<RevenueCurrencySettings role="owner" site={{ ...site, revenue_currency_state: 'conflict' }} />)
    expect(screen.getByRole('alert')).toHaveTextContent(/ordinary analytics remain available/i)
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  })
})
