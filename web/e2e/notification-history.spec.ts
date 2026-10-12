import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

const screenshotDir = 'e2e/screenshots/notify'

async function expectNoHorizontalOverflow(page: import('@playwright/test').Page) {
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
}

test('notification templates and history are responsive and accessible', async ({ page, owner }) => {
  mkdirSync(screenshotDir, { recursive: true })
  const channel = await page.request.post(`/api/private/orgs/${owner.orgId}/channels`, {
    headers: owner.apiHeaders,
    data: { kind: 'email', name: 'Operations email', enabled: true, config: { recipients: ['ops@example.test'] } },
  })
  expect(channel.ok(), await channel.text()).toBe(true)

  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, `/app/${owner.orgSlug}/settings/notifications`)
  await expect(page.getByRole('heading', { level: 1, name: 'Notifications' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Message templates' })).toBeVisible()
  await expect(page.getByText('{{.site_id}}').first()).toBeVisible()
  await expect(page.getByText('Enter a valid template to see a preview.')).toHaveCount(0)
  await expectNoHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/1440-light.png`, fullPage: true })

  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.screenshot({ path: `${screenshotDir}/1440-dark.png`, fullPage: true })
  await page.getByRole('button', { name: 'Use light theme' }).click()

  await page.setViewportSize({ width: 1024, height: 900 })
  await expectNoHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/1024.png`, fullPage: true })

  await page.setViewportSize({ width: 390, height: 844 })
  await expectNoHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/390.png`, fullPage: true })

  await page.goto(`/app/${owner.orgSlug}/${owner.siteSlug}/settings/notification-history`)
  await expect(page.getByRole('heading', { level: 1, name: 'Notification history' })).toBeVisible()
  await expect(page.getByLabel('Status')).toBeVisible()
  await expect(page.getByText('No deliveries match these filters.')).toBeVisible()
  await expectNoHorizontalOverflow(page)
})
