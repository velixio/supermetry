import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

const screenshotDir = 'e2e/screenshots/links'
const desktopUA = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'
const phoneUA = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'

async function expectNoHorizontalOverflow(page: import('@playwright/test').Page) {
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0)
}

test('short links: create, redirect safely, count clicks, show statistics and pixel', async ({ page, owner }) => {
  mkdirSync(screenshotDir, { recursive: true })
  await page.setViewportSize({ width: 1440, height: 1000 })
  const base = `/app/${owner.orgSlug}/${owner.siteSlug}/links`
  await owner.login(page, base)
  await expect(page.getByRole('heading', { level: 1, name: 'Links and pixels' })).toBeVisible()
  await expect(page.getByText('No short links yet.')).toBeVisible()

  // Other links so the list shows several states.
  const seedIDs: number[] = []
  for (const data of [
    { slug: 'launch-qr', name: 'Launch poster QR', destination: 'https://example.com/launch', enabled: false },
    { slug: 'vip-offer', name: 'VIP offer (first 1 click)', destination: 'https://example.com/vip', click_limit: 1, utm_medium: 'email' },
  ]) {
    const created = await page.request.post(`/api/private/sites/${owner.siteId}/links`, { headers: owner.apiHeaders, data })
    expect(created.status(), await created.text()).toBe(201)
    seedIDs.push((await created.json()).id)
  }
  expect((await page.request.get('/l/vip-offer', { maxRedirects: 0, headers: { 'User-Agent': desktopUA } })).status()).toBe(302)
  await expect.poll(async () => {
    const list = await (await page.request.get(`/api/private/sites/${owner.siteId}/links`)).json()
    return list.find((item: { slug: string }) => item.slug === 'vip-offer')?.status as string
  }, { timeout: 20_000 }).toBe('limit_reached')
  expect((await page.request.get('/l/vip-offer', { maxRedirects: 0 })).status()).toBe(410)
  await page.reload()

  // Create through the dialog; keyboard-reachable labelled fields.
  await page.getByRole('button', { name: 'Create link' }).click()
  const dialog = page.getByRole('dialog', { name: 'Create short link' })
  await dialog.getByLabel('Name').fill('Autumn newsletter')
  await dialog.getByLabel('Destination URL').fill('https://example.com/autumn?ref=news')
  await dialog.getByLabel('Short URL ending').fill('autumn')
  await dialog.getByLabel('Source').fill('newsletter')
  await dialog.getByLabel('Campaign').fill('autumn')
  await dialog.getByRole('button', { name: 'Save link' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('heading', { level: 2, name: /Autumn newsletter/ })).toBeVisible()
  await expect(page.getByText(/\/l\/autumn$/)).toBeVisible()

  // Unsafe destinations are refused by the server and explained in the dialog.
  await page.getByRole('button', { name: 'Create link' }).click()
  const unsafe = page.getByRole('dialog', { name: 'Create short link' })
  await unsafe.getByLabel('Name').fill('Bad')
  await unsafe.getByLabel('Destination URL').fill('javascript:alert(1)')
  await unsafe.getByRole('button', { name: 'Save link' }).click()
  await expect(page.getByRole('dialog').getByRole('alert')).toContainText(/http/)
  await page.getByRole('button', { name: 'Cancel' }).click()

  // Redirect: stored destination only, defaults appended, request parameters ignored.
  const hit = (headers: Record<string, string>) =>
    page.request.get('/l/autumn?next=https://evil.example&url=//evil.example', { maxRedirects: 0, headers })
  const first = await hit({ 'User-Agent': desktopUA, Referer: 'https://www.news.example/story?token=secret' })
  expect(first.status()).toBe(302)
  expect(first.headers()['location']).toBe('https://example.com/autumn?ref=news&utm_source=newsletter&utm_campaign=autumn')
  expect(first.headers()['cache-control']).toBe('no-store')
  for (let index = 0; index < 3; index += 1) await hit({ 'User-Agent': desktopUA, Referer: 'https://news.example/other' })
  await hit({ 'User-Agent': phoneUA })
  await hit({ 'User-Agent': 'Googlebot/2.1 (+http://www.google.com/bot.html)' }) // redirected, not counted
  expect((await page.request.get('/l/missing-slug', { maxRedirects: 0 })).status()).toBe(404)

  // Pixel always answers with an image; tagged impressions are counted.
  const me = await (await page.request.get('/api/private/me')).json()
  const publicId = me.memberships.flatMap((item: { sites: { id: number; public_id: string }[] }) => item.sites)
    .find((item: { id: number }) => item.id === owner.siteId).public_id as string
  const trackingPixel = await page.request.post(`/api/private/sites/${owner.siteId}/tracking-pixels`, {
    headers: owner.apiHeaders,
    data: { name: 'Autumn pixel', tag: 'autumn' },
  })
  expect(trackingPixel.status(), await trackingPixel.text()).toBe(201)
  const trackingPixelId = (await trackingPixel.json()).id as number
  for (let index = 0; index < 3; index += 1) {
    const pixel = await page.request.get(`/px/${publicId}.gif?c=autumn`, { headers: { 'User-Agent': desktopUA } })
    expect(pixel.status()).toBe(200)
    expect(pixel.headers()['content-type']).toBe('image/gif')
  }

  // Counts are flushed in batches; wait for them to land.
  await expect.poll(async () => {
    const list = await (await page.request.get(`/api/private/sites/${owner.siteId}/links`)).json()
    return list[0]?.clicks as number
  }, { timeout: 20_000 }).toBe(5)

  await page.reload()
  await expect(page.getByText('5', { exact: true }).first()).toBeVisible()
  await expectNoHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/1440-light.png`, fullPage: true })

  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.screenshot({ path: `${screenshotDir}/1440-dark.png`, fullPage: true })
  await page.getByRole('button', { name: 'Use light theme' }).click()

  // QR code is produced locally.
  const requests: string[] = []
  page.on('request', (request) => requests.push(request.url()))
  await page.getByRole('button', { name: 'QR code Autumn newsletter' }).click()
  await expect(page.getByRole('img', { name: /QR code that opens .*\/l\/autumn/ })).toBeVisible()
  await page.screenshot({ path: `${screenshotDir}/qr.png` })
  expect(requests.filter((url) => /qr/i.test(url) && !url.includes('/assets/'))).toEqual([])
  await page.keyboard.press('Escape')

  // Statistics view.
  await page.getByRole('link', { name: 'Statistics Autumn newsletter' }).click()
  await expect(page.getByRole('heading', { level: 1, name: /Autumn newsletter/ })).toBeVisible()
  await expect(page.getByText('news.example', { exact: true })).toBeVisible()
  await expect(page.getByText('Direct or unknown')).toBeVisible()
  await expect(page.getByText('Mobile', { exact: true })).toBeVisible()
  await expect(page.getByRole('img', { name: /5 clicks from/ })).toBeVisible()
  await expectNoHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/stats-1440-light.png`, fullPage: true })
  await page.setViewportSize({ width: 1024, height: 900 })
  await page.screenshot({ path: `${screenshotDir}/stats-1024.png`, fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await expectNoHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/stats-390.png`, fullPage: true })

  // Pixel page.
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`${base}/pixel`)
  await expect(page.getByRole('heading', { level: 1, name: 'Links and pixels' })).toBeVisible()
  await expect(page.getByRole('heading', { level: 2, name: 'Autumn pixel' })).toBeVisible()
  await page.getByRole('link', { name: 'Open Autumn pixel' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Autumn pixel' })).toBeVisible()
  await expect(page.getByText('Impressions in period: 3')).toBeVisible({ timeout: 20_000 })
  await expect(page.locator('#pixel-html')).toHaveCount(0)
  await expectNoHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/pixel-1440-light.png`, fullPage: true })
  await page.getByLabel('Period', { exact: true }).selectOption('today')
  await expect(page.getByLabel('Group by')).toHaveValue('hour')
  await expect(page.getByRole('img', { name: /^3 from .* Busiest hour .* with 3\.$/ })).toBeVisible()
  await page.screenshot({ path: `${screenshotDir}/pixel-hourly-1440-light.png`, fullPage: true })
  await page.getByLabel('Period', { exact: true }).selectOption('30d')
  await expect(page.getByLabel('Group by')).toHaveValue('day')
  await page.getByLabel('Group by').selectOption('hour')
  await expect(page.getByLabel('Period', { exact: true })).toHaveValue('7d')
  await expect(page.getByRole('img', { name: /Busiest hour .* with 3\.$/ })).toBeVisible()
  await page.getByRole('tab', { name: 'Install' }).click()
  await expect(page.locator('#pixel-html')).toContainText(`/px/${publicId}.gif?c=autumn`)
  await expectNoHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/pixel-install-1440-light.png`, fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await expectNoHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/pixel-390.png`, fullPage: true })

  // Disable stops redirecting; delete removes the link.
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(base)
  await page.getByRole('button', { name: 'Edit Autumn newsletter' }).click()
  const edit = page.getByRole('dialog', { name: 'Edit short link' })
  await edit.getByLabel('Enabled').uncheck()
  await edit.getByRole('button', { name: 'Save link' }).click()
  await expect(edit).toBeHidden()
  await expect(page.getByText('Disabled', { exact: true }).first()).toBeVisible()
  expect((await page.request.get('/l/autumn', { maxRedirects: 0 })).status()).toBe(404)
  await page.getByRole('button', { name: 'Delete Autumn newsletter' }).click()
  await page.getByRole('button', { name: 'Delete link' }).click()
  await expect(page.getByRole('heading', { level: 2, name: /Autumn newsletter/ })).toHaveCount(0)
  for (const id of seedIDs) {
    expect((await page.request.delete(`/api/private/links/${id}`, { headers: owner.apiHeaders })).status()).toBe(204)
  }
  expect((await page.request.delete(`/api/private/tracking-pixels/${trackingPixelId}`, { headers: owner.apiHeaders })).status()).toBe(204)
  await page.reload()
  await expect(page.getByText('No short links yet.')).toBeVisible()

  await page.setViewportSize({ width: 1024, height: 900 })
  await page.screenshot({ path: `${screenshotDir}/1024.png`, fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await expectNoHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/390.png`, fullPage: true })
})
