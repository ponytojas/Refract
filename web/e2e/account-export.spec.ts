import { expect, test, type Page } from '@playwright/test'
import { readFile } from 'node:fs/promises'

async function openAccountSettings(page: Page) {
  await page.addInitScript(() => localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en' })))
  await page.goto('/')
  await page.getByRole('button', { name: 'Account menu', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Settings', exact: true }).click()
  await page.getByRole('tab', { name: 'Account', exact: true }).click()
}

test('account settings downloads the ZIP when the export is ready', async ({ page, request }) => {
  const project = await request.post('/api/projects', {
    headers: { 'X-Refract': '1' },
    data: { name: 'Archive test project' },
  })
  expect(project.ok()).toBeTruthy()
  await openAccountSettings(page)
  const finished = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export my data', exact: true }).click()
  const archive = await finished
  expect(archive.suggestedFilename()).toBe('refract-account.zip')
  const content = await readFile(await archive.path())
  expect(content.subarray(0, 4)).toEqual(Buffer.from([0x50, 0x4b, 0x03, 0x04]))
})

test('an occupied export server gets a specific message', async ({ page }) => {
  await page.route('**/api/account/export', (route) => route.fulfill({ status: 503 }))
  await openAccountSettings(page)
  await page.getByRole('button', { name: 'Export my data', exact: true }).click()
  await expect(page.getByText('The server is busy preparing exports. Try again in a few minutes.')).toBeVisible()
})

test('closing account settings cancels an in-flight export poll', async ({ page }) => {
  const statusPath = '**/api/account/export/test-job'
  await page.route('**/api/account/export', (route) =>
    route.fulfill({ status: 202, json: { id: 'test-job', status: 'preparing' } }),
  )
  const pollStarted = Promise.withResolvers<void>()
  const respond = Promise.withResolvers<void>()
  await page.route(statusPath, async (route) => {
    pollStarted.resolve()
    await respond.promise
    await route.fulfill({ json: { status: 'ready' } }).catch(() => {}) // the page aborted the request
  })
  await openAccountSettings(page)
  try {
    await page.getByRole('button', { name: 'Export my data', exact: true }).click()
    await pollStarted.promise
    const aborted = page.waitForEvent('requestfailed', (request) => request.url().endsWith('/test-job'))
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog', { name: 'Settings' })).toHaveCount(0)
    await aborted
    await expect(page.getByText('Could not export your data. Try again.')).toHaveCount(0)
  } finally {
    respond.resolve()
  }
})
