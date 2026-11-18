import { expect, test } from './fixtures'

test('app project: create a metric rule, silence it, and open the history', async ({ page, owner }) => {
  await owner.login(page, `/app/${owner.orgSlug}/sites`)
  const created = await page.request.post('/api/private/sites', {
    headers: owner.apiHeaders,
    data: { name: 'Rules app', kind: 'app', timezone: 'Europe/Berlin', retention_days: 365 },
  })
  expect(created.status(), await created.text()).toBe(201)
  const site = await created.json()
  const channel = await page.request.post(`/api/private/orgs/${owner.orgId}/channels`, {
    headers: owner.apiHeaders,
    data: { kind: 'email', name: 'Operations', enabled: true, config: { recipients: ['ops@example.test'] } },
  })
  expect(channel.status(), await channel.text()).toBe(201)

  const base = `/app/${owner.orgSlug}/${site.slug}/settings`
  await page.goto(`${base}/alerts`)
  await expect(page.getByText('No alert rules yet. Rules notify you about new issues, spikes, crash rates, slow transactions and missed cron jobs.')).toBeVisible()

  await page.getByRole('button', { name: 'Create rule' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Name').fill('Too many errors')
  await dialog.getByLabel('When').selectOption('metric')
  await dialog.getByLabel('Critical threshold').fill('50')
  await dialog.getByLabel('Operations').check()
  await dialog.getByRole('button', { name: 'Save rule' }).click()
  const rule = page.getByRole('region', { name: 'Rule Too many errors' })
  await expect(rule).toContainText('Error count above 50')

  await page.goto(`${base}/alert-silences`)
  await page.getByLabel('Type').selectOption('maintenance')
  await expect(page.getByRole('status', { name: 'Next occurrences' })).toContainText('Europe/Berlin')
  await page.getByLabel('Reason (optional)').fill('weekly deploy')
  await page.getByRole('button', { name: 'Create silence' }).click()
  await expect(page.getByRole('list', { name: 'Silences' })).toContainText('weekly deploy')

  await page.goto(`${base}/notification-history`)
  await expect(page.getByRole('heading', { name: 'Notification history' })).toBeVisible()
  await page.getByLabel('Status').selectOption('suppressed')
  await expect(page.getByText('No deliveries match these filters.')).toBeVisible()
})
