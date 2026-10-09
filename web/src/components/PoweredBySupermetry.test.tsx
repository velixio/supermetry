import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import { PoweredBySupermetry } from './PoweredBySupermetry'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

test('uses the canonical homepage while runtime configuration loads', () => {
  vi.stubGlobal('fetch', vi.fn(() => new Promise(() => undefined)))
  render(<PoweredBySupermetry />)
  expect(screen.getByRole('link', { name: 'Powered by Supermetry' })).toHaveAttribute(
    'href',
    'https://supermetry.com',
  )
})

test('uses the configured public homepage without changing the instance origin', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
    homepage_url: 'https://analytics.example/about',
  }), { status: 200 })))
  render(<PoweredBySupermetry />)
  await waitFor(() => expect(screen.getByRole('link', { name: 'Powered by Supermetry' })).toHaveAttribute(
    'href',
    'https://analytics.example/about',
  ))
})
