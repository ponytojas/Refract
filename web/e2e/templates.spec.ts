import { expect, test } from '@playwright/test'

const headers = { 'X-Refract': '1' }
test.setTimeout(45_000)

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en' })))
})

test('create from a built-in template and compile', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'From a template…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New project', exact: true })
  for (const name of ['Blank project', 'Article', 'Thesis', 'Presentation', 'Letter', 'CV']) {
    await expect(dialog.getByRole('radio', { name, exact: true })).toBeVisible()
  }
  await dialog.getByRole('radio', { name: 'Article', exact: true }).check()
  await expect(dialog.getByRole('radio', { name: 'Article', exact: true })).toBeChecked()
  await dialog.getByRole('radio', { name: 'Article', exact: true }).focus()
  await page.keyboard.press('ArrowRight')
  await expect(dialog.getByRole('radio', { name: 'Thesis', exact: true })).toBeChecked()
  await dialog.getByRole('radio', { name: 'Article', exact: true }).check()
  await dialog.getByRole('textbox', { name: 'Project name', exact: true }).fill('Article from template')
  await page.screenshot({ path: test.info().outputPath('template-picker.png'), animations: 'disabled' })
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(page).toHaveURL(/\/p\/[^/]+$/)
  await page.getByRole('button', { name: 'main.tex', exact: true }).first().click()
  await expect(page.locator('.cm-content')).toContainText('Article title')
  await page.getByRole('button', { name: 'Compile', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Compile status and problems', exact: true })).toContainText(
    'Compiled',
    { timeout: 90_000 },
  )
  await expect
    .poll(() => page.locator('[data-page="1"] canvas').evaluate((canvas: HTMLCanvasElement) => canvas.width))
    .toBeGreaterThan(0)
})

test('save live edits as a private template, reuse it and remove it without changing the copy', async ({
  page,
  request,
}) => {
  const original = await (
    await request.post('/api/projects', { headers, data: { name: 'Reusable original', template: 'article' } })
  ).json()
  await request.put(`/api/projects/${original.id}/file?path=notes.txt`, { headers, data: 'Original note' })
  await page.goto(`/p/${original.id}`)
  await page.getByRole('button', { name: 'main.tex', exact: true }).first().click()
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type('\n% saved in the template')
  await page.getByRole('button', { name: 'Reusable original', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Save as template…', exact: true }).click()
  const save = page.getByRole('dialog', { name: 'Save as template', exact: true })
  await save.getByRole('textbox').fill('My article starter')
  await save.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(save).toHaveCount(0)
  await request.put(`/api/projects/${original.id}/file?path=notes.txt`, { headers, data: 'Later original edit' })
  await page.getByRole('link', { name: 'All projects', exact: true }).click()
  await expect(page.getByRole('link', { name: 'My article starter' })).toHaveCount(0)
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'From a template…', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New project', exact: true })
  await dialog.getByRole('radio', { name: 'My article starter', exact: true }).check()
  await dialog.getByRole('textbox', { name: 'Project name', exact: true }).fill('Independent copy')
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(page).toHaveURL(/\/p\/[^/]+$/)
  const copy = page.url().split('/').pop()!
  const copiedSource = await (await request.get(`/api/projects/${copy}/file?path=main.tex`, { headers })).text()
  expect(copiedSource).toContain('% saved in the template')
  expect(await (await request.get(`/api/projects/${copy}/file?path=notes.txt`, { headers })).text()).toBe(
    'Original note',
  )
  await page.getByRole('link', { name: 'All projects', exact: true }).click()
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'From a template…', exact: true }).click()
  await dialog.getByRole('button', { name: 'Delete template My article starter', exact: true }).click()
  const confirm = page.getByRole('alertdialog', { name: 'Delete template My article starter?', exact: true })
  await confirm.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(dialog.getByRole('radio', { name: 'My article starter', exact: true })).toHaveCount(0)
  expect((await request.get(`/api/projects/${copy}/file?path=main.tex`, { headers })).ok()).toBeTruthy()
})

test('the project list can save a template and still create a blank project', async ({ page, request }) => {
  const name = `List template ${Math.random().toString(36).slice(2, 8)}`
  const original = await (await request.post('/api/projects', { headers, data: { name, template: 'letter' } })).json()
  await page.goto('/')
  await page.getByRole('button', { name: `Actions for ${name}`, exact: true }).click()
  await page.getByRole('menuitem', { name: 'Save as template…', exact: true }).click()
  const save = page.getByRole('dialog', { name: 'Save as template', exact: true })
  await save.getByRole('textbox', { name: 'Template name', exact: true }).fill(name + ' starter')
  await save.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(save).toHaveCount(0)
  const templates = await (await request.get('/api/templates', { headers })).json()
  expect(
    templates.some((item: { name: string; builtin: boolean }) => item.name === name + ' starter' && !item.builtin),
  ).toBeTruthy()
  await page.getByRole('button', { name: 'New', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Blank project', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'New project', exact: true })
  await expect(dialog.getByRole('radio', { name: 'Blank project', exact: true })).toBeChecked()
  await dialog.getByRole('textbox', { name: 'Project name', exact: true }).fill('Still blank')
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(page).toHaveURL(/\/p\/[^/]+$/)
  const copy = page.url().split('/').pop()!
  expect(copy).not.toBe(original.id)
  expect(await (await request.get(`/api/projects/${copy}/files`, { headers })).json()).toEqual([])
})
