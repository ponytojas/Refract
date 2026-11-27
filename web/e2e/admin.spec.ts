import { expect, test } from '@playwright/test'

test('an administrator can create a member and delete them after confirmation', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en' })))
  await page.goto('/')
  await page.getByRole('button', { name: 'Account menu', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Account administration', exact: true }).click()
  await expect(page).toHaveURL(/\/admin$/)
  await expect(page.getByRole('heading', { name: 'Account administration', level: 1 })).toBeVisible()

  const email = 'browser-admin-fixture@example.com'
  await page.getByRole('button', { name: 'Add account', exact: true }).click()
  const dialog = page.getByRole('alertdialog')
  await dialog.getByLabel('Email', { exact: true }).fill(email)
  await dialog.getByRole('button', { name: 'Add account', exact: true }).click()
  await expect(dialog.getByRole('heading', { name: 'Generated password' })).toBeVisible()
  await dialog.getByRole('button', { name: 'Done', exact: true }).click()

  const member = page.getByRole('button', { name: email, exact: true })
  await member.click()
  await expect(page.getByRole('heading', { name: email, exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Project disk space' })).toBeVisible()
  await page.getByRole('button', { name: 'Delete account', exact: true }).click()
  await expect(dialog.getByRole('heading', { name: 'Delete this account?' })).toBeVisible()
  await expect(dialog.getByLabel('Current password', { exact: true })).toHaveCount(0)
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(member).toBeVisible()
  await page.getByRole('button', { name: 'Delete account', exact: true }).click()
  await dialog.getByRole('button', { name: 'Delete account', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(member).toHaveCount(0)
})

test('the admin action dialog asks for the administrator password', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en' })))
  const admin = { id: 'admin', email: 'admin@example.com', has_password: true, email_verified: true, is_admin: true }
  const member = { ...admin, id: 'member', email: 'member@example.com', is_admin: false }
  await page.route('**/api/me', (route) => route.fulfill({ json: { ...admin, can_sign_out: true } }))
  await page.route('**/api/admin/accounts/report?*', (route) => route.fulfill({ json: { projects: [], models: [] } }))
  await page.route('**/api/admin/accounts', async (route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ json: [admin, member] })
    } else {
      expect(route.request().postDataJSON()).toMatchObject({
        action: 'reset',
        id: member.id,
        confirmed: true,
        password: 'fixture admin password',
      })
      await route.fulfill({ json: { password: 'fixture generated password' } })
    }
  })
  await page.goto('/admin')
  await expect(page.getByRole('button', { name: 'Delete account', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: member.email, exact: true }).click()
  await page.getByRole('button', { name: 'Reset password', exact: true }).click()
  const dialog = page.getByRole('alertdialog')
  await dialog.getByLabel('Current password', { exact: true }).fill('fixture admin password')
  await dialog.getByRole('button', { name: 'Reset password', exact: true }).click()
  await expect(dialog.getByRole('heading', { name: 'Generated password' })).toBeVisible()
})
