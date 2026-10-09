import { expect, test } from './fixtures'

// Run against a fresh synthetic instance with SUPERMETRY_TRACKING_MODE=anonymous
// and SUPERMETRY_REPLAY_ENABLED=false. These are real HTTP calls, not route mocks.
// Required separate gate:
// SUPERMETRY_TRACKING_MODE=anonymous SUPERMETRY_REPLAY_ENABLED=false bun run e2e e2e/privacy.spec.ts
// The normal suite uses identified mode for its existing feature/auth fixtures.
test.skip(process.env.SUPERMETRY_TRACKING_MODE !== 'anonymous', 'Run the required separate anonymous privacy gate with SUPERMETRY_TRACKING_MODE=anonymous SUPERMETRY_REPLAY_ENABLED=false.')
test('anonymous settings cannot escalate profiles or recording', async ({ page, owner }) => {
  await owner.login(page, `/${owner.orgSlug}/${owner.siteSlug}/settings/general`)
  await expect(page.getByText(/Effective tracking: anonymous/)).toBeVisible()
  await expect(page.getByRole('switch', { name: 'Enable profiles' })).toBeDisabled()
  await expect(page.getByText(/Historical identity access is disabled/)).toBeVisible()

  const profiles = await page.request.patch(`/api/private/sites/${owner.siteId}`, {
    headers: owner.apiHeaders,
    data: { profiles_enabled: true },
  })
  expect(profiles.status()).toBe(409)

  await page.goto(`/${owner.orgSlug}/${owner.siteSlug}/settings/replay`)
  await expect(page.getByRole('switch', { name: 'Enable session replay for this project' })).toBeDisabled()
  await expect(page.getByText(/Instance replay is off/)).toBeVisible()
  const replay = await page.request.put(`/api/private/sites/${owner.siteId}/replay-settings`, {
    headers: owner.apiHeaders,
    data: { enabled: true },
  })
  expect(replay.status()).toBe(409)

  const history = await page.request.get(`/api/private/sites/${owner.siteId}/profiles`, { headers: owner.apiHeaders })
  expect(history.status()).toBe(403)
})
