import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const request = vi.hoisted(() => vi.fn(async () => ({ id: 5, key: 'pnl_abcdefgh_ABCDEFGHIJKLMNOPQRSTUVWXYZ234567ABC' })))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest: request }))

import { formatUploadToken, UploadTokenSection } from './UploadTokenSection'

afterEach(cleanup)

describe('formatUploadToken', () => {
  it('wraps the key in the sntrys_ layout with url and org in the payload', () => {
    const secret = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567ABC'
    const token = formatUploadToken(`pnl_abcdefgh_${secret}`, 'acme', 'https://app.example.test', 1700000000)
    const [prefix, payload, tail, ...extra] = token.split('_')
    expect(prefix).toBe('sntrys')
    expect(extra).toEqual([])
    expect(tail).toBe(`abcdefgh${secret}`)
    expect(tail).toHaveLength(43)
    const json = JSON.parse(atob(payload))
    expect(json).toEqual({ iat: 1700000000, url: 'https://app.example.test', region_url: 'https://app.example.test', org: 'acme' })
  })
})

describe('UploadTokenSection', () => {
  it('issues a key with only releases:write for this app and shows the token', async () => {
    render(<QueryClientProvider client={new QueryClient()}><UploadTokenSection org="acme" orgId={3} siteId={9} origin="https://app.example.test" /></QueryClientProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'Create upload token' }))
    const token = await screen.findByLabelText('Upload token')
    expect(token.textContent).toMatch(/^sntrys_.+_abcdefghABCDEFGHIJKLMNOPQRSTUVWXYZ234567ABC$/)
    expect(request).toHaveBeenCalledWith('POST', '/api/private/orgs/3/api-keys', { orgId: 3, body: { name: 'Source map upload', scopes: ['releases:write'], site_ids: [9] } })
  })
})
