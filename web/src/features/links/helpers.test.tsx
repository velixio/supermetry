import '@testing-library/jest-dom/vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { buildInput, toLocalInput } from './LinkDialog'
import LinkQr, { qrSvg } from './LinkQr'
import type { ShortLink } from './types'

const form = {
  name: ' N ', slug: ' ', destination: ' https://a.example ', utm_source: ' s ', utm_medium: '', utm_campaign: '', utm_term: '', utm_content: '',
  enabled: false, expires: '', clickLimit: ' ',
}

describe('buildInput', () => {
  it('trims values, omits an empty slug on create and clears optional limits', () => {
    expect(buildInput(form, null)).toEqual({
      name: 'N', destination: 'https://a.example', utm_source: 's', utm_medium: '', utm_campaign: '', utm_term: '', utm_content: '',
      enabled: false, expires_at: null, click_limit: null,
    })
  })

  it('sends the slug on edit only when it changed', () => {
    const existing = { slug: 'keep' } as ShortLink
    expect(buildInput({ ...form, slug: 'keep' }, existing)).not.toHaveProperty('slug')
    expect(buildInput({ ...form, slug: 'moved' }, existing)).toHaveProperty('slug', 'moved')
  })

  it('round-trips an expiry through the local datetime input', () => {
    const iso = new Date('2030-05-06T07:08:00').toISOString()
    expect(toLocalInput(iso)).toBe('2030-05-06T07:08')
    expect(toLocalInput(null)).toBe('')
    expect(toLocalInput('garbage')).toBe('')
  })
})

describe('LinkQr', () => {
  afterEach(() => cleanup())

  it('draws a quiet zone around a square module grid', () => {
    const svg = qrSvg([[true, false], [false, true]])
    expect(svg).toContain('viewBox="0 0 10 10"')
    expect(svg).toContain('M4 4h1v1h-1z')
    expect(svg).toContain('M5 5h1v1h-1z')
  })

  it('explains when the URL is too long to encode locally', () => {
    render(<LinkQr name="x" url={`https://example.com/${'a'.repeat(200)}`} />)
    expect(screen.getByRole('status')).toHaveTextContent('too long')
  })
})
