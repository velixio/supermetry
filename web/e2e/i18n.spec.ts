import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

const screenshotDir = 'e2e/screenshots/i18n'

async function noHorizontalOverflow(page: import('@playwright/test').Page, label: string) {
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth), { message: `${label} must not scroll horizontally` })
    .toBeLessThanOrEqual(0)
}

test('the pseudo-locale lengthens text without breaking layouts at 390 px', async ({ page, owner }) => {
  mkdirSync(screenshotDir, { recursive: true })
  await page.addInitScript(() => window.localStorage.setItem('supermetry.locale', 'en-XA'))
  await page.setViewportSize({ width: 390, height: 844 })
  const root = `/app/${owner.orgSlug}/${owner.siteSlug}`
  await owner.login(page, root)
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  // Catalogue text is accented, padded and bracketed, so untranslated spots stand out.
  await expect(page.getByRole('heading', { level: 1 }).first()).toHaveText(/^\[/)
  for (const [path, label] of [
    ['', 'dashboard'], ['/sessions', 'sessions'], ['/settings/general', 'site settings'], ['/settings/notifications', 'notifications'],
    ['/revenue', 'revenue'], ['/links', 'links'], ['/cohorts', 'cohorts'],
  ] as const) {
    await page.goto(`${root}${path}`)
    await expect(page.locator('main').first()).toBeVisible()
    await page.waitForTimeout(400)
    await noHorizontalOverflow(page, label)
    if (path === '') await page.screenshot({ path: `${screenshotDir}/pseudo-dashboard-390.png`, fullPage: true })
  }
  await page.goto('/app/account/profile')
  await expect(page.locator('#interface-language')).toBeVisible()
  await noHorizontalOverflow(page, 'account profile')
  await page.screenshot({ path: `${screenshotDir}/pseudo-account-390.png`, fullPage: true })
})

test('the account language preference is applied after sign in', async ({ page, owner }) => {
  await owner.login(page, '/app/account/profile')
  const saved = await page.request.patch('/api/private/me', { headers: owner.apiHeaders, data: { preferences: { locale: 'en-XA' } } })
  expect(saved.ok(), await saved.text()).toBe(true)
  try {
    // Loading the profile makes the app follow the stored preference and reload once.
    await page.reload()
    await page.waitForFunction(() => window.localStorage.getItem('supermetry.locale') === 'en-XA', undefined, { timeout: 15_000 })
    await page.waitForLoadState('load')
    await expect(page.locator('label[for="interface-language"]')).toHaveText(/^\[/, { timeout: 15_000 })
  } finally {
    const cleared = await page.request.patch('/api/private/me', { headers: owner.apiHeaders, data: { preferences: { locale: '' } } })
    expect(cleared.ok()).toBe(true)
    await page.evaluate(() => { window.localStorage.removeItem('supermetry.locale'); window.sessionStorage.clear() })
  }
})

for (const [locale, word] of [['de', /Analyse|Verhalten|Konversion/], ['es', /Análisis|Comportamiento|Conversión/], ['bn', /বিশ্লেষণ|আচরণ|রূপান্তর/]] as const) {
  test(`${locale} renders translated text, the right lang attribute and no overflow at 390 px`, async ({ page, owner }) => {
    await page.addInitScript((code) => window.localStorage.setItem('supermetry.locale', code), locale)
    await page.setViewportSize({ width: 1440, height: 900 })
    const root = `/app/${owner.orgSlug}/${owner.siteSlug}`
    await owner.login(page, root)
    await expect(page.locator('html')).toHaveAttribute('lang', locale)
    // The desktop sidebar carries catalogue text; on a phone it lives in a closed drawer.
    await expect(page.locator('body')).toContainText(word)
    await page.setViewportSize({ width: 390, height: 844 })
    for (const path of ['', '/sessions', '/settings/general', '/revenue']) {
      await page.goto(`${root}${path}`)
      await expect(page.locator('main').first()).toBeVisible()
      await page.waitForTimeout(400)
      await noHorizontalOverflow(page, `${locale} ${path || 'dashboard'}`)
    }
    mkdirSync(screenshotDir, { recursive: true })
    await page.goto(root)
    await page.waitForTimeout(500)
    await page.screenshot({ path: `${screenshotDir}/${locale}-dashboard-390.png`, fullPage: true })
  })
}
