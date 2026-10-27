import { expect, test } from '@playwright/test'

const api = { 'X-Refract': '1' }
const doc = (name: string) => `\\documentclass{article}\n\\begin{document}\n${name}.\n\\end{document}\n`

// Pages change without reloading, and nothing typed is lost on the way out,
// even when leaving before the autosave's delay, and even into another project
// (each project's edits must land in that project).
test('move between projects without reloading or losing edits', async ({ page, request }) => {
  const make = async (name: string) => {
    const p = await (await request.post('/api/projects', { headers: api, data: { name } })).json()
    await request.put(`/api/projects/${p.id}/file?path=main.tex`, { headers: api, data: doc(name) })
    return p.id as string
  }
  const a = await make('Nav Alpha')
  const b = await make('Nav Beta')
  const read = async (id: string) => (await request.get(`/api/projects/${id}/file?path=main.tex`, { headers: api })).text()

  await page.goto('/')
  await page.evaluate(() => ((window as any).__samePage = true))
  await page.getByRole('link', { name: /Nav Alpha/ }).click()
  await expect(page).toHaveURL(`/p/${a}`)
  await page.getByRole('button', { name: 'main.tex' }).first().click()

  // Type, then leave at once.
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type('\n% left right away')
  await page.getByRole('link', { name: 'All projects' }).click()
  await expect(page).toHaveURL('/')
  expect(await read(a)).toContain('% left right away')

  // Into the other project through the project menu, right after typing.
  await page.getByRole('link', { name: /Nav Alpha/ }).click()
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  await expect(page.locator('.cm-content')).toContainText('% left right away')
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type(' and more')
  await page.getByRole('button', { name: /Nav Alpha/ }).click()
  await page.getByRole('menuitem', { name: 'Nav Beta' }).click()
  await expect(page).toHaveURL(`/p/${b}`)
  expect(await read(a)).toContain('and more')
  expect(await read(b)).toBe(doc('Nav Beta'))

  // The browser's back button works, and the page was never reloaded.
  await page.goBack()
  await expect(page).toHaveURL(`/p/${a}`)
  expect(await page.evaluate(() => (window as any).__samePage)).toBe(true)
})
