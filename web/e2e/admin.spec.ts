import { expect, test } from '@playwright/test'

test('an administrator can create and remove a member through the account page', async ({ page }) => {
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
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(member).toBeVisible()
  await page.getByRole('button', { name: 'Delete account', exact: true }).click()
  await dialog.getByRole('button', { name: 'Delete account', exact: true }).click()
  await expect(member).toHaveCount(0)
})
