import { expect, test } from './fixtures'

const TRACE_ID = 'ab'.repeat(16)
const EVENT_ID = 'c1'.repeat(16)

function errorEnvelope(): string {
  const payload = {
    event_id: EVENT_ID,
    timestamp: Date.now() / 1000,
    platform: 'python',
    level: 'error',
    release: '1.0.0',
    exception: { values: [{ type: 'ValueError', value: 'payment failed', stacktrace: { frames: [{ filename: 'app/pay.py', function: 'charge', lineno: 12, in_app: true }] } }] },
    contexts: { trace: { trace_id: TRACE_ID, span_id: '1'.repeat(16) } },
  }
  const header = { event_id: EVENT_ID, sent_at: new Date().toISOString() }
  return [JSON.stringify(header), JSON.stringify({ type: 'event' }), JSON.stringify(payload)].join('\n') + '\n'
}

function transactionEnvelope(): string {
  const start = Date.now() / 1000 - 2
  const payload = {
    type: 'transaction', transaction: 'POST /pay', release: '1.0.0', platform: 'python', start_timestamp: start, timestamp: start + 0.1,
    contexts: { trace: { trace_id: TRACE_ID, span_id: '1'.repeat(16), op: 'http.server', status: 'ok' } },
    spans: [{ trace_id: TRACE_ID, span_id: '2'.repeat(16), parent_span_id: '1'.repeat(16), op: 'db', description: 'select 1', start_timestamp: start + 0.01, timestamp: start + 0.03, status: 'ok' }],
  }
  const header = { event_id: 'd2'.repeat(16), sent_at: new Date().toISOString() }
  return [JSON.stringify(header), JSON.stringify({ type: 'transaction' }), JSON.stringify(payload)].join('\n') + '\n'
}

function otlpLogs() {
  const base = BigInt(Date.now()) * 1_000_000n
  const record = (offsetMs: number, severityNumber: number, severityText: string, body: string) => ({
    timeUnixNano: String(base + BigInt(offsetMs) * 1_000_000n),
    severityNumber, severityText, body: { stringValue: body }, traceId: TRACE_ID, spanId: '1'.repeat(16),
  })
  return {
    resourceLogs: [{
      resource: { attributes: [{ key: 'service.name', value: { stringValue: 'checkout' } }] },
      scopeLogs: [{ logRecords: [record(-30, 9, 'INFO', 'cart loaded'), record(-20, 13, 'WARN', 'card retry'), record(-10, 17, 'ERROR', 'payment declined')] }],
    }],
  }
}

test('app project: error event to trace to logs, and severity search', async ({ page, owner }) => {
  await owner.login(page, `/app/${owner.orgSlug}/sites`)
  const created = await page.request.post('/api/private/sites', {
    headers: owner.apiHeaders,
    data: { name: 'Logs app', kind: 'app', timezone: 'UTC', retention_days: 365 },
  })
  expect(created.status(), await created.text()).toBe(201)
  const site = await created.json()
  const keyResponse = await page.request.post(`/api/private/sites/${site.id}/app/dsn-keys`, { headers: owner.apiHeaders, data: { label: 'e2e', allowed_origins: [] } })
  expect(keyResponse.status(), await keyResponse.text()).toBe(201)
  const { public_key: publicKey } = await keyResponse.json()

  const envelope = await page.request.post(`/api/${site.id}/envelope/?sentry_key=${publicKey}`, {
    headers: { 'Content-Type': 'application/x-sentry-envelope' }, data: errorEnvelope(),
  })
  expect(envelope.status(), await envelope.text()).toBe(200)
  const transaction = await page.request.post(`/api/${site.id}/envelope/?sentry_key=${publicKey}`, {
    headers: { 'Content-Type': 'application/x-sentry-envelope' }, data: transactionEnvelope(),
  })
  expect(transaction.status(), await transaction.text()).toBe(200)
  const logs = await page.request.post(`/api/${site.id}/otlp/v1/logs`, {
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${publicKey}` }, data: otlpLogs(),
  })
  expect(logs.status(), await logs.text()).toBe(200)

  // Open the event through its issue: the writers flush within a second or two.
  let issueId = 0
  await expect(async () => {
    const list = await page.request.get(`/api/private/sites/${site.id}/app/issues`, { headers: owner.apiHeaders })
    const body = await list.json()
    expect(body.issues?.length ?? 0).toBeGreaterThan(0)
    issueId = body.issues[0].id
  }).toPass({ timeout: 30_000 })
  await page.goto(`/app/${owner.orgSlug}/${site.slug}/issues/${issueId}/events/${EVENT_ID}`)
  await expect(page.getByRole('link', { name: 'View logs' })).toBeVisible()

  await page.getByRole('link', { name: 'View trace' }).click()
  const traceLogs = page.getByRole('list', { name: 'Logs' })
  await expect(async () => {
    await page.reload()
    await page.getByRole('tab', { name: 'Logs' }).click()
    await expect(traceLogs.getByRole('listitem')).toHaveCount(3, { timeout: 2_000 })
  }).toPass({ timeout: 30_000 })

  await page.goto(`/app/${owner.orgSlug}/${site.slug}/obs/logs`)
  await page.getByLabel('Search logs', { exact: true }).fill('severity:>=error')
  await page.getByRole('button', { name: 'Search logs' }).click()
  const rows = page.getByRole('list', { name: 'Logs' }).getByRole('listitem')
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toContainText('payment declined')
})
