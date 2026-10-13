import { expect, test } from './fixtures'

test('unknown organizations preserve sign-in intent without creating a redirect loop', async ({
  page,
  owner,
}) => {
  await page.goto('/app/missing-organization?period=30d')
  await expect(page).toHaveURL((url) => {
    return (
      url.pathname === '/login' &&
      url.searchParams.get('next') ===
        '/app/missing-organization?period=30d'
    )
  })

  await page.getByLabel('Email').fill(owner.email)
  await page.getByLabel('Password').fill(owner.password)
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page).toHaveURL(
    (url) => url.pathname === `/app/${owner.orgSlug}/${owner.siteSlug}`,
  )

  await page.goto('/app/missing-organization')
  await expect(
    page.getByText('We could not find that organisation', { exact: true }),
  ).toBeVisible()
  await expect(page.getByRole('link', { name: 'Acme', exact: true })).toHaveAttribute(
    'href',
    `/app/${owner.orgSlug}/${owner.siteSlug}`,
  )
  await expect(
    page.getByRole('link', { name: 'Dashboard', exact: true }),
  ).toHaveAttribute('href', '/app')
  await expect(page.getByRole('banner', { name: 'Application header' })).toBeVisible()
  await expect(page.getByRole('main')).toHaveCount(1)
  await expect(page).not.toHaveURL(/\/login/)
})
