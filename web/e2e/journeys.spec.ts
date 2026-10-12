import { mkdirSync } from 'node:fs'

import { expect, test } from './fixtures'

const range = { from: '2026-09-28', to: '2026-09-30' }
const shots = 'e2e/screenshots/journeys'

type Journey = {
  nodes: Array<{ step: number; path: string; visitors: number }>
  links: Array<{ from_step: number; from_path: string; to_path: string; visitors: number }>
}

test('journeys page renders the API flow without errors across viewports and themes', async ({ page, request, owner }) => {
  mkdirSync(shots, { recursive: true })
  const problems: string[] = []
  page.on('console', (message) => {
    if (message.type() === 'error') problems.push(`console: ${message.text()}`)
  })
  page.on('pageerror', (error) => problems.push(`pageerror: ${error.message}`))
  page.on('response', (response) => {
    if (response.status() >= 400) problems.push(`http ${response.status()}: ${response.url()}`)
  })

  const res = await request.post('/api/private/journeys/query', {
    headers: owner.apiHeaders,
    data: { site_id: owner.siteId, date_range: range, steps: 3, limit: 10 },
  })
  expect(res.ok(), await res.text()).toBe(true)
  const api = (await res.json()) as Journey
  expect(api.nodes.length).toBeGreaterThan(0)

  const url = `/app/${owner.orgSlug}/${owner.siteSlug}/journeys?period=custom&from=${range.from}&to=${range.to}`
  await page.setViewportSize({ width: 1440, height: 1000 })
  await owner.login(page, url)
  await expect(page.getByRole('heading', { level: 1, name: 'User journeys' })).toBeVisible()
  const diagram = page.getByRole('img', { name: 'User journey path flow' })
  await expect(diagram).toBeVisible()
  await page.screenshot({ path: `${shots}/1440-light.png`, fullPage: true })

  // Every API node is drawn, nodes never overlap inside a column, and each is tall enough for its two text lines.
  const rects = await diagram.locator('rect').evaluateAll((items) =>
    items.map((item) => ({ x: Number(item.getAttribute('x')), y: Number(item.getAttribute('y')), h: Number(item.getAttribute('height')) })),
  )
  expect(rects).toHaveLength(api.nodes.length)
  for (const rect of rects) expect(rect.h).toBeGreaterThanOrEqual(40)
  const columns = new Map<number, typeof rects>()
  for (const rect of rects) columns.set(rect.x, [...(columns.get(rect.x) ?? []), rect])
  for (const column of columns.values()) {
    const sorted = [...column].sort((a, b) => a.y - b.y)
    for (let i = 1; i < sorted.length; i += 1) expect(sorted[i].y).toBeGreaterThanOrEqual(sorted[i - 1].y + sorted[i - 1].h)
  }
  // Height contract: tallest column of node heights plus gaps plus padding (min 200), as drawn in viewBox units.
  const maxVisitors = Math.max(1, ...api.nodes.map((node) => node.visitors))
  const expectedHeight = Math.max(200, 48 + Math.max(...[...new Set(api.nodes.map((node) => node.step))].map((step) => {
    const column = api.nodes.filter((node) => node.step === step)
    return column.reduce((sum, node) => sum + Math.max(40, (node.visitors / maxVisitors) * 92), 0) + 12 * (column.length - 1)
  })))
  const box = await diagram.boundingBox()
  expect(box!.width).toBeGreaterThan(0)
  await expect(diagram.locator('title').first()).toContainText('visitors')
  await expect(page.getByRole('table', { name: 'Journey links' })).toHaveCount(1)

  // Controls drive the request: steps and the start page reach the API.
  const stepped = page.waitForRequest((candidate) => candidate.url().endsWith('/journeys/query') && candidate.postDataJSON().steps === 4)
  await page.getByLabel('Steps').selectOption('4')
  await stepped
  const anchored = page.waitForRequest((candidate) => candidate.url().endsWith('/journeys/query') && candidate.postDataJSON().start_path === '/pricing')
  await page.getByLabel('Start page').fill('/pricing')
  await page.getByLabel('Start page').press('Enter')
  await anchored
  await expect(diagram).toBeVisible()
  await page.getByLabel('Start page').fill('')
  await page.getByRole('button', { name: 'Apply' }).click()
  await page.getByLabel('Steps').selectOption('3')
  await expect(diagram.locator('rect')).toHaveCount(api.nodes.length)

  await page.getByRole('button', { name: 'Use dark theme' }).click()
  await page.screenshot({ path: `${shots}/1440-dark.png`, fullPage: true })
  await page.getByRole('button', { name: 'Use light theme' }).click()

  for (const width of [1024, 390]) {
    await page.setViewportSize({ width, height: 900 })
    const probe = await page.evaluate(() => {
      const tall = [...document.querySelectorAll('body *')]
        .map((el) => ({ tag: el.tagName, cls: String(el.className).slice(0, 60), top: el.getBoundingClientRect().top + scrollY, h: el.getBoundingClientRect().height, bottom: el.getBoundingClientRect().bottom + scrollY }))
        .sort((a, b) => b.bottom - a.bottom).slice(0, 4)
      return { scrollHeight: document.documentElement.scrollHeight, tall }
    })
    const cardBottom = await page.getByRole('region', { name: 'Journey results' }).evaluate((el) => el.getBoundingClientRect().bottom + scrollY)
    expect(probe.scrollHeight - cardBottom, `blank space below the card at ${width}: ${JSON.stringify(probe.tall[0])}`).toBeLessThan(300)
    const svgHeight = await diagram.evaluate((el) => el.getBoundingClientRect().height / (el.getBoundingClientRect().width / 960))
    expect(Math.abs(svgHeight - expectedHeight)).toBeLessThan(1)
    if (width === 390) {
      const fontPx = await diagram.locator('text').first().evaluate((el) => (el as SVGGraphicsElement).getBoundingClientRect().height)
      expect(fontPx).toBeGreaterThanOrEqual(10)
      await expect(page.getByText('Scroll sideways to see all steps.')).toBeVisible()
    }
    console.log(width, probe.scrollHeight)
    await page.screenshot({ path: `${shots}/${width}.png`, fullPage: true })
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    expect(overflow, `horizontal page overflow at ${width}`).toBeLessThanOrEqual(0)
  }
  expect(problems).toEqual([])
})
