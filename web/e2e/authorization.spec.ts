import type { APIRequestContext, Page } from '@playwright/test'
import { expect, test, type Owner } from './fixtures'

const password = 'panel-authorization-test-password'

async function createMember(
  request: APIRequestContext,
  owner: Owner,
  email: string,
  access: {
    site_ids: number[]
    capabilities: string[]
    billing_admin?: boolean
  },
) {
  const invitation = await request.post(
    `/api/private/orgs/${owner.orgId}/invitations`,
    {
      headers: owner.apiHeaders,
      data: { email, role: 'viewer', ...access },
    },
  )
  expect(invitation.ok(), await invitation.text()).toBe(true)
  const { link } = (await invitation.json()) as { link: string }
  const token = new URL(link).pathname.split('/').at(-1)
  const accepted = await request.post(
    `/api/private/invitations/${token}/accept`,
    { data: { name: 'Authorization test', password } },
  )
  expect(accepted.ok(), await accepted.text()).toBe(true)
  return ((await accepted.json()) as { user_id: number }).user_id
}

async function login(page: Page, email: string) {
  await page.goto('/login')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login/)
}

function recordSensitiveReads(page: Page) {
  const requests: string[] = []
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname
    if (
      request.method() === 'GET' &&
      /\/api\/private\/(?:profiles(?:\/|$)|sites\/[^/]+\/(?:profiles|replays)(?:\/|$))/.test(
        path,
      )
    )
      requests.push(path)
  })
  return requests
}

test('billing-only no-site member reaches organization and account pages without analytics requests', async ({
  page,
  owner,
}) => {
  const email = 'billing-only@panel.test'
  await createMember(page.request, owner, email, {
    site_ids: [],
    capabilities: [],
    billing_admin: true,
  })
  const reads = recordSensitiveReads(page)
  await login(page, email)
  await expect(page).toHaveURL(`/${owner.orgSlug}/settings`)
  await expect(
    page.getByText('No project data access.', { exact: false }),
  ).toBeVisible()
  await expect(
    page.getByRole('heading', { name: 'Billing access' }),
  ).toBeVisible()
  await expect(page.locator('[data-slot="sidebar-wrapper"]')).toBeVisible()
  for (const name of ['Dashboard', 'Profiles', 'Replays'])
    await expect(page.getByRole('link', { name, exact: true })).toHaveCount(0)
  await expect(
    page.getByRole('heading', { name: 'Create your first site' }),
  ).toHaveCount(0)
  await page.screenshot({
    path: 'e2e/screenshots/authorization-billing-only.png',
    fullPage: true,
  })
  await page
    .getByRole('link', { name: 'Account settings', exact: true })
    .click()
  await expect(page).toHaveURL('/account/profile')
  expect(reads).toEqual([])
})

test('stats-only viewer cannot fetch sensitive direct URLs or see export and replay controls', async ({
  page,
  owner,
}) => {
  const email = 'stats-only@panel.test'
  await createMember(page.request, owner, email, {
    site_ids: [owner.siteId],
    capabilities: [],
  })
  const reads = recordSensitiveReads(page)
  await login(page, email)
  await expect(
    page.getByRole('button', { name: 'Export CSV', exact: true }),
  ).toHaveCount(0)
  for (const name of ['Profiles', 'Replays'])
    await expect(page.getByRole('link', { name, exact: true })).toHaveCount(0)
  for (const suffix of ['profiles', 'profiles/123', 'replays', 'replays/123']) {
    await page.goto(`/${owner.orgSlug}/${owner.siteSlug}/${suffix}`)
    await expect(
      page.getByRole('alert').filter({ hasText: 'You don’t have permission' }),
    ).toBeVisible()
  }
  await page.goto(
    `/${owner.orgSlug}/${owner.siteSlug}/sessions?has_replay=true&replay=123`,
  )
  await expect(
    page.getByRole('heading', { name: 'Sessions', exact: true }),
  ).toBeVisible()
  await expect(page.getByLabel('Has replay')).toHaveCount(0)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect(reads).toEqual([])
})

test('revoking grants and all project access removes sensitive controls after membership refresh', async ({
  page,
  owner,
}) => {
  const email = 'revoked-grants@panel.test'
  const member = await createMember(page.request, owner, email, {
    site_ids: [owner.siteId],
    capabilities: ['profiles.read', 'replay.read', 'data.export'],
  })
  await login(page, email)
  await expect(
    page.getByRole('link', { name: 'Profiles', exact: true }),
  ).toBeVisible()
  await expect(
    page.getByRole('button', { name: 'Export CSV', exact: true }),
  ).toBeVisible()
  const changed = await page.request.patch(
    `/api/private/orgs/${owner.orgId}/members/${member}`,
    { headers: owner.apiHeaders, data: { capabilities: [] } },
  )
  expect(changed.ok(), await changed.text()).toBe(true)
  await page.reload()
  await expect(
    page.getByRole('link', { name: 'Dashboard', exact: true }),
  ).toBeVisible()
  await expect(
    page.getByRole('link', { name: 'Profiles', exact: true }),
  ).toHaveCount(0)
  await expect(
    page.getByRole('button', { name: 'Export CSV', exact: true }),
  ).toHaveCount(0)
  const restricted = await page.request.patch(
    `/api/private/orgs/${owner.orgId}/members/${member}`,
    { headers: owner.apiHeaders, data: { site_ids: [] } },
  )
  expect(restricted.ok(), await restricted.text()).toBe(true)
  await page.goto('/')
  await expect(page).toHaveURL(`/${owner.orgSlug}/settings`)
  await expect(
    page.getByText('No project data access.', { exact: false }),
  ).toBeVisible()
  await expect(
    page.getByRole('link', { name: 'Dashboard', exact: true }),
  ).toHaveCount(0)
})

test('owner invitation controls submit independent billing-only access', async ({
  page,
  owner,
}) => {
  await owner.login(page, `/${owner.orgSlug}/settings/invites`)
  await page
    .getByRole('textbox', { name: 'Email', exact: true })
    .fill('billing-invite-ui@panel.test')
  await page.getByLabel('Site access', { exact: true }).selectOption('none')
  await page.getByRole('checkbox', { name: /Billing Admin/ }).check()
  for (const name of [/Read profiles/, /Watch replays/, /Export data/])
    await expect(page.getByRole('checkbox', { name })).not.toBeChecked()
  const pending = page.waitForRequest(
    (request) =>
      request.method() === 'POST' &&
      request.url().endsWith(`/orgs/${owner.orgId}/invitations`),
  )
  await page.getByRole('button', { name: 'Invite', exact: true }).click()
  expect((await pending).postDataJSON()).toEqual({
    email: 'billing-invite-ui@panel.test',
    role: 'viewer',
    site_ids: [],
    capabilities: [],
    billing_admin: true,
  })
  await expect(
    page.getByRole('button', { name: 'Copy invitation link' }),
  ).toBeVisible()
  await page.screenshot({
    path: 'e2e/screenshots/authorization-invite-controls.png',
    fullPage: true,
  })
})
