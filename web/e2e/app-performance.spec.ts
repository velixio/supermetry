import { expect, test } from './fixtures'

const TRACE_ID = 'f'.repeat(32)

function transactionEnvelope(): string {
  const now = Date.now() / 1000
  const start = now - 2
  const payload = {
    type: 'transaction',
    transaction: 'GET /items',
    release: '1.0.0',
    platform: 'python',
    start_timestamp: start,
    timestamp: start + 0.1,
    contexts: { trace: { trace_id: TRACE_ID, span_id: '1'.repeat(16), op: 'http.server', status: 'ok' } },
    spans: [
      { trace_id: TRACE_ID, span_id: '2'.repeat(16), parent_span_id: '1'.repeat(16), op: 'db', description: 'select * from items where id = 42', start_timestamp: start + 0.01, timestamp: start + 0.03, status: 'ok' },
      { trace_id: TRACE_ID, span_id: '3'.repeat(16), parent_span_id: '1'.repeat(16), op: 'http.client', description: 'GET https://api.example.com/v1/items/42', start_timestamp: start + 0.04, timestamp: start + 0.09, status: 'ok' },
    ],
  }
  const header = { event_id: 'a1'.repeat(16), sent_at: new Date().toISOString() }
  return [JSON.stringify(header), JSON.stringify({ type: 'transaction' }), JSON.stringify(payload)].join('\n') + '\n'
}

test('app project: a Sentry transaction shows in the performance list and as a waterfall', async ({ page, owner }) => {
  await owner.login(page, `/app/${owner.orgSlug}/sites`)
  const created = await page.request.post('/api/private/sites', {
    headers: owner.apiHeaders,
    data: { name: 'Perf app', kind: 'app', timezone: 'UTC', retention_days: 365 },
  })
  expect(created.status(), await created.text()).toBe(201)
  const site = await created.json()

  const keyResponse = await page.request.post(`/api/private/sites/${site.id}/app/dsn-keys`, { headers: owner.apiHeaders, data: { label: 'e2e', allowed_origins: [] } })
  expect(keyResponse.status(), await keyResponse.text()).toBe(201)
  const { public_key: publicKey } = await keyResponse.json()

  const intake = await page.request.post(`/api/${site.id}/envelope/?sentry_key=${publicKey}`, {
    headers: { 'Content-Type': 'application/x-sentry-envelope' },
    data: transactionEnvelope(),
  })
  expect(intake.status(), await intake.text()).toBe(200)

  // Spans are written within a second; open hours are read from the raw table.
  await page.goto(`/app/${owner.orgSlug}/${site.slug}/performance`)
  await expect(page.getByRole('heading', { name: 'Performance' })).toBeVisible()
  const table = page.getByRole('table', { name: 'Transactions' })
  await expect(async () => {
    await page.reload()
    await expect(table).toContainText('GET /items', { timeout: 2_000 })
  }).toPass({ timeout: 30_000 })

  await table.getByRole('link', { name: 'GET /items' }).click()
  await expect(page.getByRole('heading', { name: 'GET /items' })).toBeVisible()
  const spans = page.getByRole('table', { name: 'Span groups' })
  await expect(spans).toContainText('select * from items where id = ?')
  await expect(spans).not.toContainText('42')

  await page.getByRole('table', { name: 'Slowest samples' }).getByRole('link').first().click()
  await expect(page.getByRole('heading', { name: 'Trace' })).toBeVisible()
  await expect(page.getByRole('list', { name: 'Spans' }).getByRole('listitem')).toHaveCount(3)
  await expect(page.getByTestId('span-bar').nth(1)).toHaveAttribute('data-left', /^(?!0\.0$)/)
})
