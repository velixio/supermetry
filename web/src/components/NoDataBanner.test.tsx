import '@testing-library/jest-dom/vitest'

import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { NoDataBanner } from './NoDataBanner'
import { renderWithRouter } from '@/test/renderWithRouter'

describe('NoDataBanner', () => {
  it('links an eventless site to its installation steps', async () => {
    renderWithRouter(<NoDataBanner href="/acme/docs/settings/installation" />)
    expect(await screen.findByText('No data yet')).toBeVisible()
    expect(screen.getByRole('link', { name: /view installation steps/i })).toHaveAttribute(
      'href',
      '/acme/docs/settings/installation',
    )
  })
})
