import { expect, test } from '@playwright/test'

const doc = '\\documentclass{article}\n\\begin{document}\nHello from the smoke test.\n\\end{document}\n'
const api = { 'X-Refract': '1' }

// Open project → edit → compile → see PDF.
test('open project, edit, compile, see the PDF', async ({ page, request }) => {
  const project = await (await request.post('/api/projects', { headers: api, data: { name: 'Smoke' } })).json()
  expect((await request.put(`/api/projects/${project.id}/file?path=main.tex`, { headers: api, data: doc })).ok()).toBeTruthy()
  expect((await request.patch(`/api/projects/${project.id}`, { headers: api, data: { main_file: 'main.tex' } })).ok()).toBeTruthy()

  await page.goto(`/p/${project.id}`)
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  const editor = page.locator('.cm-content')
  await editor.click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type('% edited in the browser\n')
  await page.keyboard.press('ControlOrMeta+Enter') // saves, then compiles

  await expect(page.getByRole('button', { name: 'Compile status and problems' })).toContainText(/Compiled/, { timeout: 90_000 })
  const canvas = page.locator('[data-page="1"] canvas')
  await expect.poll(() => canvas.evaluate((c: HTMLCanvasElement) => c.width)).toBeGreaterThan(0)

  // The edit reached the disk before compiling.
  const saved = await (await request.get(`/api/projects/${project.id}/file?path=main.tex`, { headers: api })).text()
  expect(saved).toContain('% edited in the browser')
})

test('import named fonts and bibliography, compile, and keep the source', async ({ page, request }) => {
  const source = `\\documentclass{article}
\\usepackage{fontspec}
\\IfFontExistsTF{Open Sans}{\\setmainfont{Open Sans}}{\\errmessage{Open Sans not resolved}}
\\setmonofont{Latin Modern Mono}
\\begin{document}
Open Sans: áéíóú ñ. \\textbf{Bold} \\textit{Italic} \\textbf{\\textit{Bold italic}}.
\\par {\\ttfamily Latin Modern Mono.}
A citation: \\cite{example2024}.
\\bibliographystyle{plain}
\\bibliography{references}
\\end{document}
`
  const response = await request.post('/api/projects/import-folder?name=Named%20fonts', {
    headers: api,
    multipart: {
      file: { name: 'main.tex', mimeType: 'application/x-tex', buffer: Buffer.from(source) },
      bibliography: { name: 'references.bib', mimeType: 'application/x-bibtex', buffer: Buffer.from('@article{example2024,author={Example, Ada},title={Synthetic bibliography test},journal={Example Journal},year={2024}}') },
    },
  })
  expect(response.ok()).toBeTruthy()
  const imported = await response.json()
  expect(imported.warnings).toEqual([])
  await page.goto(`/p/${imported.project.id}`)
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+Enter')
  await expect(page.getByRole('button', { name: 'Compile status and problems' })).toContainText(/Compiled/, { timeout: 90_000 })
  await expect.poll(() => page.locator('[data-page="1"] canvas').evaluate((c: HTMLCanvasElement) => c.width)).toBeGreaterThan(0)
  const saved = await request.get(`/api/projects/${imported.project.id}/file?path=main.tex`, { headers: api })
  expect(await saved.text()).toBe(source)
  const checks = await request.get(`/api/projects/${imported.project.id}/checks`, { headers: api })
  expect(await checks.json()).toEqual([])
  const log = await (await request.get(`/api/projects/${imported.project.id}/log`, { headers: api })).text()
  expect(log).toContain('main.bbl')
  expect(log).not.toContain('No file main.bbl')
  expect(log).not.toContain('undefined')
})
