import '@testing-library/jest-dom/vitest'

import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { NoDataBanner } from './NoDataBanner'

describe('NoDataBanner', () => {
  it('links an eventless site to its installation steps', () => {
    render(<NoDataBanner href="/acme/docs/settings/installation" />)
    expect(screen.getByText('No data yet')).toBeVisible()
    expect(screen.getByRole('link', { name: /view installation steps/i })).toHaveAttribute(
      'href',
      '/acme/docs/settings/installation',
    )
  })
})
