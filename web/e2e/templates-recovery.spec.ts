import { expect, test } from '@playwright/test'

const headers = { 'X-Refract': '1' }
test.setTimeout(30_000)
test.beforeEach(async ({ page }, info) => {
  await page.addInitScript(
    (language) => localStorage.setItem('refract-prefs', JSON.stringify({ language })),
    info.title.includes('Spanish') ? 'es' : 'en',
  )
})

test('template loading can be retried without losing the project name', async ({ page }) => {
  let attempts = 0
  await page.route('**/api/templates', async (route) => {
    if (attempts++ === 0)
      await route.fulfill({
        status: 503,
        json: { error: { kind: 'other', message: 'Template list is temporarily unavailable.' } },
      })
    else await route.continue()
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'From a template…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New project', exact: true })
  await dialog.getByRole('textbox', { name: 'Project name', exact: true }).fill('Do not lose this name')
  await expect(dialog.getByRole('alert')).toHaveText('Template list is temporarily unavailable.')
  await dialog.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(dialog.getByRole('radio', { name: 'Article', exact: true })).toBeVisible()
  await expect(dialog.getByRole('alert')).toHaveCount(0)
  await expect(dialog.getByRole('textbox', { name: 'Project name', exact: true })).toHaveValue('Do not lose this name')
})

test('a failed project creation preserves the selected template and can be retried', async ({ page }) => {
  let attempts = 0
  await page.route('**/api/projects', async (route) => {
    if (route.request().method() === 'POST' && attempts++ === 0)
      await route.fulfill({
        status: 503,
        json: { error: { kind: 'other', message: 'Try creating the project again.' } },
      })
    else await route.continue()
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'From a template…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New project', exact: true })
  await dialog.getByRole('radio', { name: 'Letter', exact: true }).check()
  await dialog.getByRole('textbox', { name: 'Project name', exact: true }).fill('Recovered letter')
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(page.getByText('Try creating the project again.', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('radio', { name: 'Letter', exact: true })).toBeChecked()
  await expect(dialog.getByRole('textbox', { name: 'Project name', exact: true })).toHaveValue('Recovered letter')
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(page).toHaveURL(/\/p\/[^/]+$/)
  await page.getByRole('button', { name: 'main.tex', exact: true }).first().click()
  await expect(page.locator('.cm-content')).toContainText('Subject of your letter')
  expect(attempts).toBe(2)
})

test('failed workspace saves preserve the dialog and recover without creating duplicate templates', async ({
  page,
  request,
}) => {
  const p = await (
    await request.post('/api/projects', { headers, data: { name: 'Retry save', template: 'cv' } })
  ).json()
  const name = `Save retry ${p.id}`
  let attempts = 0
  await page.route(`**/api/projects/${p.id}/template`, async (route) => {
    if (attempts++ === 0)
      await route.fulfill({
        status: 503,
        json: { error: { kind: 'other', message: 'Try saving the template again.' } },
      })
    else await route.continue()
  })
  await page.goto(`/p/${p.id}`)
  await page.getByRole('button', { name: 'Retry save', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Save as template…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Save as template', exact: true })
  await dialog.getByRole('textbox').fill(name)
  await dialog.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Try saving the template again.', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('textbox')).toHaveValue(name)
  await expect(dialog.getByRole('button', { name: 'Save', exact: true })).toBeEnabled()
  await dialog.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  const templates = await (await request.get('/api/templates', { headers })).json()
  expect(templates.filter((item: { name: string }) => item.name === name)).toHaveLength(1)
})

test('cancelling project creation or template deletion does not write or remove anything', async ({
  page,
  request,
}) => {
  const p = await (
    await request.post('/api/projects', { headers, data: { name: 'Cancel source', template: 'thesis' } })
  ).json()
  const template = await (
    await request.post(`/api/projects/${p.id}/template`, { headers, data: { name: `Cancel template ${p.id}` } })
  ).json()
  let writes = 0
  page.on('request', (req) => {
    if (req.method() === 'POST' || req.method() === 'DELETE') writes++
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'From a template…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New project', exact: true })
  await dialog.getByRole('radio', { name: template.name, exact: true }).check()
  await dialog.getByRole('button', { name: `Delete template ${template.name}`, exact: true }).click()
  const confirm = page.getByRole('alertdialog', { name: `Delete template ${template.name}?`, exact: true })
  await confirm.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(dialog.getByRole('radio', { name: template.name, exact: true })).toBeChecked()
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  expect(writes).toBe(0)
  expect((await request.get(`/api/projects/${template.id}`, { headers })).ok()).toBeTruthy()
})

test('Spanish template picker works at a narrow viewport without horizontal overflow', async ({ page, request }) => {
  const p = await (
    await request.post('/api/projects', { headers, data: { name: 'Mobile source', template: 'article' } })
  ).json()
  const longName = `Plantilla privada ñ ${'a'.repeat(100)}`
  await request.post(`/api/projects/${p.id}/template`, { headers, data: { name: longName } })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.goto('/')
  await page.getByRole('button', { name: 'Nuevo', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Desde una plantilla…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Nuevo proyecto', exact: true })
  for (const name of ['Proyecto en blanco', 'Artículo', 'Tesis', 'Presentación', 'Carta', 'CV']) {
    await expect(dialog.getByRole('radio', { name, exact: true })).toBeVisible()
  }
  await dialog.getByRole('radio', { name: longName, exact: true }).check()
  await expect(dialog.getByRole('textbox', { name: 'Nombre del proyecto', exact: true })).toBeInViewport()
  await expect(dialog.getByRole('button', { name: 'Crear', exact: true })).toBeInViewport()
  await dialog.getByRole('radio', { name: 'Artículo', exact: true }).check()
  await expect(dialog.getByRole('radio', { name: 'Artículo', exact: true })).toBeChecked()
  await dialog.getByRole('textbox', { name: 'Nombre del proyecto', exact: true }).fill('Artículo español')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  expect(await dialog.evaluate((el) => el.scrollLeft)).toBe(0)
  expect(await dialog.getByRole('radiogroup').evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  await expect(dialog.getByRole('textbox', { name: 'Nombre del proyecto', exact: true })).toBeInViewport()
  await expect(dialog.getByRole('button', { name: 'Crear', exact: true })).toBeInViewport()
  await page.screenshot({ path: test.info().outputPath('templates-spanish-mobile-dark.png'), animations: 'disabled' })
  await dialog.getByRole('button', { name: 'Crear', exact: true }).click()
  await expect(page).toHaveURL(/\/p\/[^/]+$/)
})
