import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

const screenshotDir = 'e2e/screenshots/install-recipes'

async function noHorizontalOverflow(page: import('@playwright/test').Page) {
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth), { message: 'page must not scroll horizontally' })
    .toBeLessThanOrEqual(0)
}

test('install recipes show real snippets, switch platforms and verify the installation', async ({ page, owner }) => {
  mkdirSync(screenshotDir, { recursive: true })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, `/app/${owner.orgSlug}/${owner.siteSlug}/settings/installation`)
  await expect(page.getByRole('heading', { level: 1, name: 'Installation' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Install recipes' })).toBeVisible()

  const origin = new URL(page.url()).origin
  await expect(page.getByLabel('Site ID', { exact: true })).toHaveText(owner.sitePublicID)
  await expect(page.getByLabel('Host', { exact: true })).toHaveText(origin)
  const prompt = page.getByLabel('AI install prompt')
  await expect(prompt).toContainText(`Site ID: ${owner.sitePublicID}`)
  await expect(prompt).toContainText(`Host: ${origin}`)

  const html = page.getByLabel('HTML snippet')
  await expect(html).toContainText(`data-site="${owner.sitePublicID}"`)
  await expect(html).toContainText('/js/')

  await page.getByRole('tab', { name: 'Next.js' }).click()
  await expect(page.getByLabel('Next.js install command')).toHaveText('npm install @supermetry/nextjs')
  await expect(page.getByLabel('Next.js snippet')).toContainText(`site="${owner.sitePublicID}" host="${origin}"`)
  await page.getByRole('tab', { name: 'Go', exact: true }).click()
  await expect(page.getByLabel('Go snippet')).toContainText('/api/public/v1/events')
  await page.getByRole('tab', { name: 'Google Tag Manager' }).click()
  await expect(page.getByLabel('Google Tag Manager snippet')).toContainText('createElement')

  await noHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/1440-light.png`, fullPage: true })
  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await page.screenshot({ path: `${screenshotDir}/1440-dark.png`, fullPage: true })
  await page.getByRole('button', { name: 'Use light theme' }).click()

  // The fixture events are old, so verification waits until a new event arrives.
  await page.getByRole('button', { name: 'Verify installation' }).click()
  await expect(page.getByText(/Waiting for the first event/)).toBeVisible()
  const sent = await page.request.post('/api/public/v1/events', {
    headers: { 'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) Chrome/126.0 Safari/537.36', Origin: 'https://site1.test' },
    data: { site: owner.siteSlug, name: 'pageview', url: 'https://site1.test/install-check' },
  })
  expect(sent.status()).toBe(202)
  await expect(page.getByText('Events are arriving. Installation verified.')).toBeVisible({ timeout: 30_000 })

  await page.setViewportSize({ width: 1024, height: 900 })
  await noHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/1024.png`, fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await noHorizontalOverflow(page)
  await page.screenshot({ path: `${screenshotDir}/390.png`, fullPage: true })
})
