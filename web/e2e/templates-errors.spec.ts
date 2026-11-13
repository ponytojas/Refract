import { expect, test } from '@playwright/test'

const headers = { 'X-Refract': '1' }
test.setTimeout(30_000)
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en' })))
})

test('project creation stays single while a slow response is pending', async ({ page, request }) => {
  const name = `Pending creation ${Math.random().toString(36).slice(2)}`
  let release!: () => void
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  let submissions = 0
  await page.route('**/api/projects', async (route) => {
    if (route.request().method() === 'POST') {
      submissions++
      await pending
    }
    await route.continue()
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'From a template…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New project', exact: true })
  await dialog.getByRole('radio', { name: 'Article', exact: true }).check()
  await dialog.getByRole('textbox', { name: 'Project name', exact: true }).fill(name)
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  try {
    await expect.poll(() => submissions).toBe(1)
    await expect(dialog.getByRole('button', { name: 'Create', exact: true })).toBeDisabled()
    await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeDisabled()
    await page.keyboard.press('Enter')
    await page.keyboard.press('Escape')
    await expect(dialog).toBeVisible()
    expect(submissions).toBe(1)
  } finally {
    release()
  }
  await expect(page).toHaveURL(/\/p\/[^/]+$/)
  const projects = await (await request.get('/api/projects', { headers })).json()
  expect(projects.filter((item: { name: string }) => item.name === name)).toHaveLength(1)
})

test('slow template deletion blocks duplicate requests and closing its confirmation', async ({ page, request }) => {
  const p = await (
    await request.post('/api/projects', { headers, data: { name: 'Slow delete source', template: 'article' } })
  ).json()
  const template = await (
    await request.post(`/api/projects/${p.id}/template`, { headers, data: { name: `Slow delete ${p.id}` } })
  ).json()
  let release!: () => void
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  let submissions = 0
  await page.route(`**/api/projects/${template.id}`, async (route) => {
    if (route.request().method() === 'DELETE') {
      submissions++
      await pending
    }
    await route.continue()
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'From a template…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New project', exact: true })
  await dialog.getByRole('button', { name: `Delete template ${template.name}`, exact: true }).click()
  const confirm = page.getByRole('alertdialog', { name: `Delete template ${template.name}?`, exact: true })
  await confirm.getByRole('button', { name: 'Delete', exact: true }).click()
  try {
    await expect.poll(() => submissions).toBe(1)
    await expect(confirm.getByRole('button', { name: 'Delete', exact: true })).toBeDisabled()
    await expect(confirm.getByRole('button', { name: 'Cancel', exact: true })).toBeDisabled()
    await page.keyboard.press('Enter')
    await page.keyboard.press('Escape')
    await expect(confirm).toBeVisible()
    expect(submissions).toBe(1)
  } finally {
    release()
  }
  await expect(confirm).toHaveCount(0)
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('radio', { name: template.name, exact: true })).toHaveCount(0)
})

test('saving from the workspace prevents duplicate submissions while the request is pending', async ({
  page,
  request,
}) => {
  const p = await (
    await request.post('/api/projects', { headers, data: { name: 'Single save', template: 'article' } })
  ).json()
  const name = `Only one snapshot ${p.id}`
  let release!: () => void
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  let submissions = 0
  await page.route(`**/api/projects/${p.id}/template`, async (route) => {
    submissions++
    await pending
    await route.continue()
  })
  await page.goto(`/p/${p.id}`)
  await page.getByRole('button', { name: 'Single save', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Save as template…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Save as template', exact: true })
  await dialog.getByRole('textbox').fill(name)
  await dialog.getByRole('button', { name: 'Save', exact: true }).click()
  try {
    await expect.poll(() => submissions).toBe(1)
    await expect(dialog.getByRole('button', { name: 'Save', exact: true })).toBeDisabled({ timeout: 1500 })
    await expect(dialog.getByRole('textbox')).toBeDisabled()
    await page.keyboard.press('Enter')
    await page.keyboard.press('Escape')
    await expect(dialog).toBeVisible()
    expect(submissions).toBe(1)
  } finally {
    release()
  }
  await expect(dialog).toHaveCount(0)
  const templates = await (await request.get('/api/templates', { headers })).json()
  expect(templates.filter((item: { name: string }) => item.name === name)).toHaveLength(1)
})

test('failed deletion is visible inside its confirmation, retries and resets a deleted selection', async ({
  page,
  request,
}) => {
  const p = await (
    await request.post('/api/projects', { headers, data: { name: 'Delete source', template: 'article' } })
  ).json()
  const template = await (
    await request.post(`/api/projects/${p.id}/template`, { headers, data: { name: `Delete retry starter ${p.id}` } })
  ).json()
  let attempts = 0
  await page.route(`**/api/projects/${template.id}`, async (route) => {
    if (route.request().method() === 'DELETE' && attempts++ === 0) {
      await route.fulfill({
        status: 503,
        json: { error: { kind: 'other', message: 'Cannot delete this template yet.' } },
      })
    } else await route.continue()
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'From a template…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New project', exact: true })
  await dialog.getByRole('textbox', { name: 'Project name', exact: true }).fill('Keep this name')
  await dialog.getByRole('radio', { name: template.name, exact: true }).check()
  await dialog.getByRole('button', { name: `Delete template ${template.name}`, exact: true }).click()
  const confirm = page.getByRole('alertdialog', { name: `Delete template ${template.name}?`, exact: true })
  await confirm.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(confirm.getByRole('alert')).toHaveText('Cannot delete this template yet.', { timeout: 1500 })
  await expect(confirm.getByRole('alert')).toBeVisible()
  await expect(confirm.getByRole('button', { name: 'Delete', exact: true })).toBeEnabled()
  await confirm.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(confirm).toHaveCount(0)
  await expect(dialog.getByRole('radio', { name: template.name, exact: true })).toHaveCount(0)
  await expect(dialog.getByRole('radio', { name: 'Blank project', exact: true })).toBeChecked()
  await expect(dialog.getByRole('textbox', { name: 'Project name', exact: true })).toHaveValue('Keep this name')
})
