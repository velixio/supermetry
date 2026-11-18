import { expect, type Page } from '@playwright/test'

/**
 * Sets the theme through the app's `D` shortcut (the toggle itself now lives in the account menu,
 * which is inside the drawer on phones). Does nothing when the theme already matches.
 */
export async function setTheme(page: Page, theme: 'light' | 'dark') {
  const isDark = () => page.evaluate(() => document.documentElement.classList.contains('dark'))
  if ((await isDark()) !== (theme === 'dark')) {
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    await page.keyboard.press('d')
  }
  await expect.poll(isDark).toBe(theme === 'dark')
}
