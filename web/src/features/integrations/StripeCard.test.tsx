import { renderAuthorized as render } from '@/test/authorization'
import '@testing-library/jest-dom/vitest'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { privateRequest } from '@/features/settings/privateApi'
import { en } from '@/messages/en'

import { IntegrationsPage } from './IntegrationsPage'
import { absoluteWebhookURL, stripeState } from './StripeCard'
import type { StripeIntegration } from './api'

vi.mock('@/features/settings/privateApi', () => ({ privateRequest: vi.fn() }))

const request = vi.mocked(privateRequest)
const s = en.integrationsUi.stripe
const path = '/api/private/sites/7/integrations/stripe'

const off: StripeIntegration = {
  enabled: false, secret_configured: false, record_payment_intents: false, events: [], optional_events: [],
  events_received: 0, revenue_events: 0, refunds_30d: 0, refunded_micros_30d: 0,
}

const pending: StripeIntegration = {
  ...off,
  enabled: true,
  webhook_url: '/api/public/v1/integrations/stripe/webhook/we_abc123',
  events: ['checkout.session.completed', 'invoice.paid', 'charge.refunded'],
  optional_events: ['payment_intent.succeeded'],
  revenue_currency: 'EUR',
  created_at: '2026-11-01T00:00:00Z',
}

const active: StripeIntegration = {
  ...pending,
  secret_configured: true,
  events_received: 12,
  revenue_events: 9,
  refunds_30d: 2,
  refunded_micros_30d: 15_500_000,
  last_event_at: '2026-11-02T10:00:00Z',
  last_event_type: 'invoice.paid',
  last_event_livemode: true,
}

function serve(initial: StripeIntegration) {
  let current = initial
  request.mockImplementation(async (method, url, options) => {
    if (String(url).endsWith('/integrations')) return { data: [] }
    if (url !== path) return undefined
    switch (method) {
      case 'GET':
        return current
      case 'POST':
        current = pending
        return current
      case 'PATCH': {
        const body = (options as { body?: { signing_secret?: string; record_payment_intents?: boolean } }).body ?? {}
        current = {
          ...current,
          secret_configured: current.secret_configured || Boolean(body.signing_secret),
          record_payment_intents: body.record_payment_intents ?? current.record_payment_intents,
        }
        return current
      }
      case 'DELETE':
        current = off
        return undefined
    }
    return undefined
  })
}

function renderPage(role = 'owner') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <IntegrationsPage orgId={3} role={role} siteId={7} />
    </QueryClientProvider>,
  )
}

function card() {
  return screen.getByRole('region', { name: s.title })
}

describe('stripe helpers', () => {
  it('derives the card state', () => {
    expect(stripeState(undefined)).toBe('off')
    expect(stripeState(off)).toBe('off')
    expect(stripeState(pending)).toBe('pending')
    expect(stripeState(active)).toBe('active')
    expect(stripeState({ ...active, last_error: 'currency mismatch' })).toBe('error')
  })

  it('prefixes relative webhook URLs with the dashboard origin', () => {
    expect(absoluteWebhookURL('/api/public/v1/integrations/stripe/webhook/x', 'https://stats.example.test'))
      .toBe('https://stats.example.test/api/public/v1/integrations/stripe/webhook/x')
    expect(absoluteWebhookURL('https://a.example.test/w/x', 'https://b.example.test')).toBe('https://a.example.test/w/x')
  })
})

describe('stripe card', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('renders next to an empty catalog and enables Stripe', async () => {
    serve(off)
    renderPage()
    expect(await screen.findByText(en.integrationsUi.notConfiguredTitle)).toBeInTheDocument()
    fireEvent.click(await within(card()).findByRole('button', { name: s.enable }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('POST', path, { orgId: 3 }))
    const url = await within(card()).findByLabelText(s.webhookUrl)
    expect(url).toHaveValue(`${window.location.origin}/api/public/v1/integrations/stripe/webhook/we_abc123`)
    expect(within(card()).getByText('checkout.session.completed')).toBeInTheDocument()
    expect(within(card()).getByText('payment_intent.succeeded')).toBeInTheDocument()
    expect(within(card()).getByText(s.statusPending)).toBeInTheDocument()
  })

  it('validates and saves the signing secret', async () => {
    serve(pending)
    renderPage()
    const input = await within(card()).findByLabelText(s.secretLabel)
    expect(input).toHaveAttribute('type', 'password')
    fireEvent.change(input, { target: { value: 'not-a-secret' } })
    fireEvent.click(within(card()).getByRole('button', { name: s.saveSecret }))
    expect(await within(card()).findByText(s.secretRequired)).toBeInTheDocument()
    expect(request).not.toHaveBeenCalledWith('PATCH', path, expect.anything())

    fireEvent.change(input, { target: { value: 'whsec_abcdefghijklmnopqrstuvwxyz012345' } })
    fireEvent.click(within(card()).getByRole('button', { name: s.saveSecret }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('PATCH', path, {
      orgId: 3, body: { signing_secret: 'whsec_abcdefghijklmnopqrstuvwxyz012345' },
    }))
    expect(await within(card()).findByText(s.statusActive)).toBeInTheDocument()
    expect(input).toHaveValue('')
  })

  it('toggles payment intents', async () => {
    serve(active)
    renderPage()
    fireEvent.click(await within(card()).findByRole('checkbox', { name: s.paymentIntents }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('PATCH', path, { orgId: 3, body: { record_payment_intents: true } }))
  })

  it('shows status, refunds and errors', async () => {
    serve({ ...active, last_error: 'payment currency USD differs from site currency EUR', last_event_livemode: false })
    renderPage()
    expect(await within(card()).findByText(s.statusError)).toBeInTheDocument()
    expect(within(card()).getByText(s.lastError('payment currency USD differs from site currency EUR'))).toBeInTheDocument()
    expect(within(card()).getByText(s.testMode)).toBeInTheDocument()
    expect(within(card()).getByText('12')).toBeInTheDocument()
    expect(within(card()).getByText(/^2 for .*15\.50/)).toBeInTheDocument()
  })

  it('warns when the site has no revenue currency', async () => {
    serve({ ...pending, revenue_currency: undefined })
    renderPage()
    expect(await within(card()).findByText(s.currencyMissing)).toBeInTheDocument()
  })

  it('disconnects after confirmation', async () => {
    serve(active)
    renderPage()
    fireEvent.click(await within(card()).findByRole('button', { name: s.disconnect }))
    const dialog = await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog).getByRole('button', { name: s.disconnectConfirm }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('DELETE', path, { orgId: 3 }))
    expect(await within(card()).findByRole('button', { name: s.enable })).toBeInTheDocument()
  })

  it('is read-only for editors and hidden for viewers', async () => {
    serve(active)
    renderPage('editor')
    expect(await within(card()).findByText(s.statusActive)).toBeInTheDocument()
    expect(within(card()).getByText(s.readOnly)).toBeInTheDocument()
    expect(within(card()).queryByLabelText(s.secretLabel)).not.toBeInTheDocument()
    expect(within(card()).queryByRole('button', { name: s.disconnect })).not.toBeInTheDocument()
    expect(within(card()).queryByLabelText(s.webhookUrl)).not.toBeInTheDocument()
    cleanup()
    request.mockClear()

    renderPage('viewer')
    expect(await screen.findByText(en.integrationsUi.notConfiguredTitle)).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: s.title })).not.toBeInTheDocument()
    expect(request).not.toHaveBeenCalledWith('GET', path, expect.anything())
  })
})
