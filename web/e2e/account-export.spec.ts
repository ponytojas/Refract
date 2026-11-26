import { expect, test } from '@playwright/test'
import { readFile } from 'node:fs/promises'

test('account settings downloads the ZIP when the export is ready', async ({ page, request }) => {
  await page.addInitScript(() => localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en' })))
  const project = await request.post('/api/projects', {
    headers: { 'X-Refract': '1' },
    data: { name: 'Archive test project' },
  })
  expect(project.ok()).toBeTruthy()
  await page.goto('/')
  await page.getByRole('button', { name: 'Account menu', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Settings', exact: true }).click()
  await page.getByRole('tab', { name: 'Account', exact: true }).click()
  const finished = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export my data', exact: true }).click()
  const archive = await finished
  expect(archive.suggestedFilename()).toBe('refract-account.zip')
  const content = await readFile(await archive.path())
  expect(content.subarray(0, 4)).toEqual(Buffer.from([0x50, 0x4b, 0x03, 0x04]))
})
