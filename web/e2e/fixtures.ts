import { execFileSync } from 'node:child_process'
import { mkdirSync, readFileSync, rmSync } from 'node:fs'

import {
  expect,
  request as playwrightRequest,
  test as base,
  type APIResponse,
  type BrowserContext,
  type FullConfig,
  type Page,
} from '@playwright/test'

const email = 'owner@supermetry.test'
const password = 'supermetry-e2e-password'

export type Owner = {
  schema: string
  email: string
  password: string
  orgId: number
  orgSlug: string
  siteId: number
  siteSlug: string
  shareSlug: string
  apiHeaders: Record<string, string>
  login(page: Page, next?: string): Promise<void>
}

type SerializedOwner = Omit<Owner, 'login'>

function required(name: string): string {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is required for the e2e suite`)
  return value
}

function value<T>(record: Record<string, unknown>, snake: string, title: string): T {
  const result = record[snake] ?? record[title]
  if (result === undefined) throw new Error(`missing ${snake} in e2e API response`)
  return result as T
}

export function fmtCompact(input: number): string {
  return Math.abs(input) >= 10_000
    ? new Intl.NumberFormat('en-US', {
        notation: 'compact',
        maximumFractionDigits: 1,
      }).format(input)
    : new Intl.NumberFormat('en-US', { maximumFractionDigits: 2 }).format(input)
}

async function responseJSON<T>(response: APIResponse): Promise<T> {
  const body = await response.text()
  expect(response.ok(), body).toBe(true)
  return JSON.parse(body) as T
}

export default async function globalSetup(config: FullConfig) {
  const baseURL = String(config.projects[0]?.use.baseURL)
  const resetURL = readFileSync(required('SUPERMETRY_E2E_RESET_FILE'), 'utf8').trim()
  const token = new URL(resetURL).searchParams.get('token')
  if (!token) throw new Error('supermetry user create did not return a reset token')

  const api = await playwrightRequest.newContext({ baseURL, extraHTTPHeaders: { Origin: baseURL } })
  const reset = await api.post('/api/private/auth/password-reset/confirm', {
    data: { token, password },
  })
  const resetBody = await reset.text()
  expect(reset.ok(), resetBody).toBe(true)

  const login = await api.post('/api/private/auth/login', {
    data: { email, password, device_label: 'Playwright setup' },
  })
  const loginBody = await login.text()
  expect(login.ok(), loginBody).toBe(true)

  const state = await api.storageState()
  const csrf = state.cookies.find((cookie) => cookie.name === 'supermetry_csrf')?.value
  if (!csrf) throw new Error('login did not set the CSRF cookie')
  const cookie = state.cookies.map((item) => `${item.name}=${item.value}`).join('; ')
  const authHeaders = { Cookie: cookie, 'X-CSRF-Token': csrf }

  const organizations = await api.get('/api/private/orgs', { headers: authHeaders })
  const organizationBody = await responseJSON<{
    data: Array<Record<string, unknown>>
  }>(organizations)
  const membership = organizationBody.data[0]
  if (!membership) throw new Error('owner has no organization')
  const orgId = value<number>(membership, 'org_id', 'OrgID')
  const orgSlug = value<string>(membership, 'org_slug', 'OrgSlug')
  const apiHeaders = { ...authHeaders, 'X-Supermetry-Org': String(orgId) }

  const createdSite = await api.post('/api/private/sites', {
    headers: apiHeaders,
    data: {
      name: 'Site 1',
      hosts: ['site1.test'],
      timezone: 'America/New_York',
      retention_days: 30,
      revenue_currency: 'USD',
    },
  })
  const site = await responseJSON<Record<string, unknown>>(createdSite)
  const siteId = value<number>(site, 'id', 'ID')
  const siteSlug = value<string>(site, 'public_id', 'PublicID')

  const createdShare = await api.post(`/api/private/sites/${siteId}/shares`, {
    headers: apiHeaders,
    data: {
      password: '',
      config: {
        sections: ['kpis', 'chart', 'pages', 'sources', 'locations', 'devices', 'where', 'weekly_rhythm', 'goals'],
        filters: [],
        date: { mode: 'rolling', days: 30, max_days: 30 },
        theme: 'system',
        embed: { enabled: false, allowed_origins: [], confirm_wildcard: false },
      },
    },
  })
  const share = await responseJSON<Record<string, unknown>>(createdShare)
  const shareSlug = value<string>(share, 'slug', 'Slug')

  const owner: SerializedOwner = {
    schema: required('SUPERMETRY_E2E_SCHEMA'),
    email,
    password,
    orgId,
    orgSlug,
    siteId,
    siteSlug,
    shareSlug,
    apiHeaders,
  }
  process.env.SUPERMETRY_E2E_OWNER = JSON.stringify(owner)
  mkdirSync('e2e/screenshots', { recursive: true })
  await api.dispose()

  return async () => {
    const testDSN = required('SUPERMETRY_TEST_DSN')
    const schema = required('SUPERMETRY_E2E_SCHEMA')
    const serverPIDFile = required('SUPERMETRY_E2E_SERVER_PID_FILE')
    const serverPID = Number(readFileSync(serverPIDFile, 'utf8').trim())
    if (!Number.isSafeInteger(serverPID) || serverPID <= 1) throw new Error('invalid E2E server PID')
    try {
      process.kill(serverPID, 'SIGTERM')
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw error
    }
    const shutdownDeadline = Date.now() + 5_000
    while (Date.now() < shutdownDeadline) {
      try {
        process.kill(serverPID, 0)
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code === 'ESRCH') break
        throw error
      }
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    try {
      process.kill(serverPID, 0)
      throw new Error('E2E server did not stop before schema cleanup')
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw error
    }
    execFileSync('psql', [
      testDSN,
      '-v',
      'ON_ERROR_STOP=1',
      '-c',
      `drop schema if exists "${schema}" cascade`,
    ])
    rmSync(required('SUPERMETRY_E2E_RESET_FILE'), { force: true })
    rmSync(required('SUPERMETRY_E2E_SITES_FILE'), { force: true })
    rmSync(serverPIDFile, { force: true })
  }
}

let refreshedAPIHeaders: Record<string, string> | undefined
let browserCookies: Parameters<BrowserContext['addCookies']>[0] | undefined

export const test = base.extend<{ owner: Owner }>({
  owner: async ({ request }, use) => {
    const serialized = JSON.parse(required('SUPERMETRY_E2E_OWNER')) as SerializedOwner
    let apiHeaders = refreshedAPIHeaders ?? serialized.apiHeaders
    const probe = await request.get('/api/private/me', { headers: apiHeaders })
    if (probe.status() === 401) {
      const login = await request.post('/api/private/auth/login', {
        data: { email: serialized.email, password: serialized.password, device_label: 'Playwright API fixture' },
      })
      const loginBody = await login.text()
      expect(login.ok(), loginBody).toBe(true)
      const state = await request.storageState()
      const csrf = state.cookies.find((cookie) => cookie.name === 'supermetry_csrf')?.value
      if (!csrf) throw new Error('refreshed API login did not set the CSRF cookie')
      const cookie = state.cookies.map((item) => `${item.name}=${item.value}`).join('; ')
      apiHeaders = { Cookie: cookie, 'X-CSRF-Token': csrf, 'X-Supermetry-Org': String(serialized.orgId) }
      refreshedAPIHeaders = apiHeaders
    } else {
      expect(probe.ok(), await probe.text()).toBe(true)
    }
    await use({
      ...serialized,
      apiHeaders,
      async login(page, next = '/') {
        const destination = new URL(next, 'http://supermetry.test')
        const reachedDestination = (url: URL) =>
          next === '/'
            ? url.pathname !== '/login'
            : url.pathname === destination.pathname &&
              [...destination.searchParams].every(
                ([name, value]) => url.searchParams.get(name) === value,
              )
        if (browserCookies) {
          await page.context().addCookies(browserCookies)
          const probe = await page.request.get('/api/private/me')
          if (probe.ok()) {
            await page.goto(next)
            await page.waitForURL(reachedDestination)
            return
          }
          browserCookies = undefined
          await page.context().clearCookies()
        }
        await page.goto(`/login?next=${encodeURIComponent(next)}`)
        const emailInput = page.getByLabel('Email')
        await expect(emailInput).toBeVisible()
        await emailInput.fill(serialized.email)
        await page.getByLabel('Password').fill(serialized.password)
        const responsePromise = page.waitForResponse(
          (response) =>
            response.url().endsWith('/api/private/auth/login') &&
            response.request().method() === 'POST',
        )
        const destinationPromise = page.waitForURL(reachedDestination)
        const [response] = await Promise.all([
          responsePromise,
          destinationPromise,
          page.getByRole('button', { name: 'Sign in' }).click(),
        ])
        expect(response.ok()).toBe(true)
        browserCookies = await page.context().cookies()
      },
    })
  },
})

export { expect } from '@playwright/test'
