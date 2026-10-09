import { execFileSync } from 'node:child_process'
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
  const token = new URL(link).searchParams.get('token')
  expect(token, 'issued invitation must contain a query token').toBeTruthy()
  const accepted = await request.post(
    `/api/private/invitations/${encodeURIComponent(token!)}/accept`,
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
  ).toHaveCount(0)
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
  // Recording now requires explicit project opt-in as well as instance permission.
  // Run this identified-mode authorization fixture with PANEL_REPLAY_ENABLED=true.
  const replayOptIn = await page.request.put(`/api/private/sites/${owner.siteId}/replay-settings`, {
    headers: owner.apiHeaders,
    data: { enabled: true },
  })
  expect(replayOptIn.ok(), await replayOptIn.text()).toBe(true)
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
  for (const feature of ['profiles', 'replays']) {
    const response = page.waitForResponse((response) =>
      new URL(response.url()).pathname === `/api/private/sites/${owner.siteId}/${feature}`,
    )
    await page.goto(`/${owner.orgSlug}/${owner.siteSlug}/${feature}`)
    expect((await response).ok()).toBe(true)
    await expect(page.getByRole('heading', { name: feature === 'profiles' ? 'Profiles' : 'Replays', exact: true })).toBeVisible()
  }
  await page.goto(`/${owner.orgSlug}/${owner.siteSlug}`)
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

test('organization switching isolates cached profiles from a no-site membership', async ({ page, owner }) => {
  const email = 'switching@panel.test'
  const member = await createMember(page.request, owner, email, {
    site_ids: [owner.siteId], capabilities: ['profiles.read', 'data.export'],
  })
  const schema = owner.schema
  const dsn = process.env.PANEL_TEST_DSN
  if (!schema || !/^panel_e2e_\d+$/.test(schema) || !dsn) throw new Error('isolated e2e database is required')
  // Seed only the disposable schema owned by this Playwright run.
  execFileSync('psql', [dsn, '-v', 'ON_ERROR_STOP=1', '-v', `schema=${schema}`, '-v', `member=${member}`, '-v', `site=${owner.siteId}`], {
    input: `SET search_path TO :"schema";
WITH organization AS (INSERT INTO orgs(name,slug) VALUES ('Restricted organization','restricted-org') RETURNING id)
INSERT INTO memberships(org_id,user_id,role,site_ids) SELECT id,:member,'viewer','{}'::integer[] FROM organization;
INSERT INTO profiles(site_id,external_id,first_seen_at,last_seen_at) VALUES (:site,'Acme-only-profile',now(),now());`,
    stdio: ['pipe', 'pipe', 'pipe'],
  })
  const bootstrap = page.waitForResponse((response) =>
    new URL(response.url()).pathname === '/api/private/me',
  )
  await login(page, email)
  expect((await bootstrap).status(), 'multi-organization session must bootstrap without an organization header').toBe(200)
  await page.getByRole('link', { name: 'Profiles', exact: true }).click()
  await expect(page.getByText('Acme-only-profile', { exact: true })).toBeVisible()
  const reads = recordSensitiveReads(page)
  await page.getByRole('button', { name: 'Acme', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Restricted organization', exact: true }).click()
  await expect(page).toHaveURL('/restricted-org/settings')
  await expect(page.getByText('Acme-only-profile', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('link', { name: 'Profiles', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Export CSV', exact: true })).toHaveCount(0)
  expect(reads).toEqual([])
  await page.getByRole('button', { name: 'Restricted organization', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Acme', exact: true }).click()
  await page.getByRole('link', { name: 'Profiles', exact: true }).click()
  await expect(page.getByText('Acme-only-profile', { exact: true })).toBeVisible()
  expect(reads).toContain(`/api/private/sites/${owner.siteId}/profiles`)
})

test('owner invitation controls copy a working no-site invitation without billing fields', async ({
  page,
  browser,
  owner,
}) => {
  await owner.login(page, `/${owner.orgSlug}/settings/invites`)
  await page.getByRole('combobox', { name: 'Site access', exact: true }).selectOption('none')
  await expect(page.getByRole('checkbox', { name: /Billing Admin/ })).toHaveCount(0)
  for (const name of [/Read profiles/, /Watch replays/, /Export data/])
    await expect(page.getByRole('checkbox', { name })).not.toBeChecked()
  const inviteEmail = page.getByRole('textbox', { name: 'Email', exact: true })
  await inviteEmail.fill('billing-invite-ui@panel.test')
  await expect(inviteEmail).toHaveValue('billing-invite-ui@panel.test')
  const pending = page.waitForRequest(
    (request) =>
      request.method() === 'POST' &&
      request.url().endsWith(`/orgs/${owner.orgId}/invitations`),
  )
  const created = page.waitForResponse(
    (response) => response.request().method() === 'POST' &&
      response.url().endsWith(`/orgs/${owner.orgId}/invitations`),
  )
  await page.getByRole('button', { name: 'Invite', exact: true }).click()
  expect((await pending).postDataJSON()).toEqual({
    email: 'billing-invite-ui@panel.test',
    role: 'viewer',
    site_ids: [],
    capabilities: [],
  })
  await expect(
    page.getByRole('button', { name: 'Copy invitation link' }),
  ).toBeVisible()
  const { link } = await (await created).json() as { link: string }
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.getByRole('button', { name: 'Copy invitation link' }).click()
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied).toBe(link)
  const token = new URL(copied).searchParams.get('token')
  expect(token).toBeTruthy()
  const guest = await browser.newContext()
  try {
    const invitationPage = await guest.newPage()
    await invitationPage.goto(copied)
    await expect(invitationPage).toHaveURL(new URL(`/invite/${token}`, copied).href)
    await invitationPage.getByLabel('Name', { exact: true }).fill('Billing invite')
    await invitationPage.getByLabel('Password', { exact: true }).fill(password)
    const accepted = invitationPage.waitForResponse((response) =>
      response.request().method() === 'POST' && response.url().endsWith(`/invitations/${token}/accept`),
    )
    await invitationPage.getByRole('button', { name: 'Accept invitation' }).click()
    expect((await accepted).ok()).toBe(true)
  } finally {
    await guest.close()
  }
  await page.screenshot({
    path: 'e2e/screenshots/authorization-invite-controls.png',
    fullPage: true,
  })
})
