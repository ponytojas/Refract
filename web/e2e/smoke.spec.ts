import { expect, test } from '@playwright/test'

const doc = '\\documentclass{article}\n\\begin{document}\nHello from the smoke test.\n\\end{document}\n'
const api = { 'X-Refract': '1' }

// Open project → edit → compile → see PDF.
test('open project, edit, compile, see the PDF', async ({ page, request }) => {
  const project = await (await request.post('/api/projects', { headers: api, data: { name: 'Smoke' } })).json()
  expect(
    (await request.put(`/api/projects/${project.id}/file?path=main.tex`, { headers: api, data: doc })).ok(),
  ).toBeTruthy()
  expect(
    (await request.patch(`/api/projects/${project.id}`, { headers: api, data: { main_file: 'main.tex' } })).ok(),
  ).toBeTruthy()

  await page.goto(`/p/${project.id}`)
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  const editor = page.locator('.cm-content')
  await editor.click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type('% edited in the browser\n')
  await page.keyboard.press('ControlOrMeta+Enter') // saves, then compiles

  await expect(page.getByRole('button', { name: 'Compile status and problems' })).toContainText(/Compiled/, {
    timeout: 90_000,
  })
  const canvas = page.locator('[data-page="1"] canvas')
  await expect.poll(() => canvas.evaluate((c: HTMLCanvasElement) => c.width)).toBeGreaterThan(0)

  // The edit reached the disk before compiling.
  const saved = await (await request.get(`/api/projects/${project.id}/file?path=main.tex`, { headers: api })).text()
  expect(saved).toContain('% edited in the browser')
})

test('autocomplete \\ref from the labels of the project, refreshed on save', async ({ page, request }) => {
  const project = await (await request.post('/api/projects', { headers: api, data: { name: 'Autocomplete' } })).json()
  const source = '\\documentclass{article}\n\\begin{document}\n\\section{Intro}\\label{sec:intro}\n'
  expect(
    (await request.put(`/api/projects/${project.id}/file?path=main.tex`, { headers: api, data: source })).ok(),
  ).toBeTruthy()

  const symbols = page.waitForResponse((r) => r.url().endsWith('/symbols'))
  await page.goto(`/p/${project.id}`)
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  await symbols
  const editor = page.locator('.cm-content')
  await editor.click()
  await page.keyboard.press('ControlOrMeta+End')

  await page.keyboard.type('\\ref{')
  await page.getByRole('option', { name: 'sec:intro' }).click() // Enter is ignored for a moment after opening
  await expect(editor).toContainText('\\ref{sec:intro}')
  await expect(editor).not.toContainText('}}')

  // A label reaches the list once it is saved.
  const refreshed = page.waitForResponse((r) => r.url().endsWith('/symbols'))
  await page.keyboard.type('\n\\label{sec:new}\n')
  await refreshed
  await page.keyboard.type('\\ref{')
  await expect(page.getByRole('option', { name: 'sec:new' })).toBeVisible()

  // Commands explain themselves: their arguments in the list, a description beside it.
  await page.keyboard.press('Escape')
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type('\n\\fra')
  await expect(page.getByRole('option', { name: /frac/ })).toContainText('{numerator}{denominator}')
  await expect(page.locator('.cm-completionInfo')).toHaveText('A fraction.')

  // And stay in view while their arguments are filled in.
  await page.getByRole('option', { name: /frac/ }).click()
  const argument = page.locator('.cm-signatureHint-active')
  await expect(argument).toHaveText('{numerator}')
  await page.keyboard.type('a')
  await page.keyboard.press('Tab')
  await expect(argument).toHaveText('{denominator}')

  // A finished command keeps its help, and Enter still breaks the line.
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type('\n\\begin')
  await expect(page.locator('.cm-signatureHint')).toContainText('Opens an environment.')
  await expect(page.locator('.cm-tooltip-autocomplete')).toBeHidden()
  await page.keyboard.press('Enter')
  await expect(page.locator('.cm-signatureHint')).toBeHidden()
  await expect(page.locator('.cm-line').last()).toHaveText('')
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
      bibliography: {
        name: 'references.bib',
        mimeType: 'application/x-bibtex',
        buffer: Buffer.from(
          '@article{example2024,author={Example, Ada},title={Synthetic bibliography test},journal={Example Journal},year={2024}}',
        ),
      },
    },
  })
  expect(response.ok()).toBeTruthy()
  const imported = await response.json()
  expect(imported.warnings).toEqual([])
  await page.goto(`/p/${imported.project.id}`)
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+Enter')
  await expect(page.getByRole('button', { name: 'Compile status and problems' })).toContainText(/Compiled/, {
    timeout: 90_000,
  })
  await expect
    .poll(() => page.locator('[data-page="1"] canvas').evaluate((c: HTMLCanvasElement) => c.width))
    .toBeGreaterThan(0)
  const saved = await request.get(`/api/projects/${imported.project.id}/file?path=main.tex`, { headers: api })
  expect(await saved.text()).toBe(source)
  const checks = await request.get(`/api/projects/${imported.project.id}/checks`, { headers: api })
  expect(await checks.json()).toEqual([])
  const log = await (await request.get(`/api/projects/${imported.project.id}/log`, { headers: api })).text()
  expect(log).toContain('main.bbl')
  expect(log).not.toContain('No file main.bbl')
  expect(log).not.toContain('undefined')
})

test('click a section in the outline moves the cursor', async ({ page, request }) => {
  const project = await (await request.post('/api/projects', { headers: api, data: { name: 'Paper' } })).json()
  const files: Record<string, string> = {
    'paper/main.tex':
      '\\documentclass{article}\n\\begin{document}\n\\section{Introduction}\nText.\n\\input{chapters/method}\n\\section{Results}\nMore.\n\\end{document}\n',
    'paper/chapters/method.tex': 'Lead-in.\n\\section{Method}\n\\subsection{Data}\nBody.\n',
  }
  for (const [path, data] of Object.entries(files))
    expect(
      (
        await request.put(`/api/projects/${project.id}/file?path=${encodeURIComponent(path)}`, { headers: api, data })
      ).ok(),
    ).toBeTruthy()
  expect(
    (await request.patch(`/api/projects/${project.id}`, { headers: api, data: { main_file: 'paper/main.tex' } })).ok(),
  ).toBeTruthy()

  await page.goto(`/p/${project.id}`)
  // The outline sits under the file tree, open by default, and collapses from its header.
  const toggle = page.getByRole('button', { name: 'Outline', exact: true })
  const outline = page.getByRole('navigation', { name: 'Outline' })
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await expect(outline).toBeHidden()
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  const items = outline.getByRole('button')
  await expect(items).toHaveCount(4)
  await expect
    .poll(() => items.evaluateAll((bs) => bs.map((b) => b.getAttribute('aria-label'))))
    .toEqual(['Introduction', 'Method', 'Data', 'Results'])
  // Headings from an included file carry its path; those of the main file don't.
  await expect(outline.getByRole('button', { name: 'Method', exact: true })).toHaveAccessibleDescription(
    'Method · paper/chapters/method.tex:2',
  )
  await expect(outline.getByRole('button', { name: 'Method', exact: true })).toContainText('method.tex')
  await expect(outline.getByRole('button', { name: 'Results', exact: true })).not.toContainText('.tex')

  // A heading in an included file opens that file on its line, and becomes the current one.
  const data = outline.getByRole('button', { name: 'Data', exact: true })
  await data.click()
  await expect(page.locator('.cm-activeLine')).toHaveText('\\subsection{Data}')
  await expect(data).toHaveAttribute('aria-current', 'true')

  const results = outline.getByRole('button', { name: 'Results', exact: true })
  await results.click()
  await expect(page.locator('.cm-activeLine')).toHaveText('\\section{Results}')
  await expect(results).toHaveAttribute('aria-current', 'true')

  // Rename a heading in the included file, close its tab (which saves it), and
  // collapse and expand the outline: it shows the saved title, on its line.
  await data.click()
  await expect(page.locator('.cm-activeLine')).toHaveText('\\subsection{Data}')
  await page.keyboard.press('End')
  await page.keyboard.press('Shift+Home')
  await page.keyboard.type('\\subsection{Samples}')
  await expect(page.locator('.cm-activeLine')).toHaveText('\\subsection{Samples}')
  await page.getByRole('button', { name: 'Close paper/chapters/method.tex', exact: true }).click()
  await expect
    .poll(async () =>
      (await request.get(`/api/projects/${project.id}/file?path=paper/chapters/method.tex`, { headers: api })).text(),
    )
    .toContain('\\subsection{Samples}')
  await toggle.click()
  await expect(outline).toBeHidden()
  await toggle.click()
  await expect
    .poll(() => items.evaluateAll((bs) => bs.map((b) => b.getAttribute('aria-label'))))
    .toEqual(['Introduction', 'Method', 'Samples', 'Results'])
  await expect(outline.getByRole('button', { name: 'Samples', exact: true })).toHaveAccessibleDescription(
    'Samples · paper/chapters/method.tex:3',
  )
})

// A project that goes to the trash while open: the workspace locks, keeps what was typed, and saves it on restore.
test('a trashed project locks the workspace, and restoring saves the pending edits', async ({ page, request }) => {
  const project = await (await request.post('/api/projects', { headers: api, data: { name: 'Trashed' } })).json()
  expect(
    (await request.put(`/api/projects/${project.id}/file?path=main.tex`, { headers: api, data: doc })).ok(),
  ).toBeTruthy()
  expect(
    (await request.patch(`/api/projects/${project.id}`, { headers: api, data: { main_file: 'main.tex' } })).ok(),
  ).toBeTruthy()

  await page.goto(`/p/${project.id}`)
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  const editor = page.locator('.cm-content')
  await editor.click()
  await page.keyboard.press('ControlOrMeta+End')

  expect((await request.delete(`/api/projects/${project.id}`, { headers: api })).ok()).toBeTruthy()
  await page.keyboard.type('% typed while trashed\n') // its autosave learns the project is gone

  const banner = page.getByRole('status').filter({ hasText: 'This project is in the trash' })
  await expect(banner).toBeVisible()
  await expect(editor).toHaveAttribute('aria-readonly', 'true')
  await expect(page.getByRole('button', { name: 'Compile', exact: true })).toBeDisabled()
  const before = await editor.innerText()
  await page.keyboard.type('more text that must not appear')
  expect(await editor.innerText()).toBe(before)

  await banner.getByRole('button', { name: 'Restore' }).click()
  await expect(banner).toBeHidden()
  await expect(editor).not.toHaveAttribute('aria-readonly', 'true')
  await expect
    .poll(async () => (await request.get(`/api/projects/${project.id}/file?path=main.tex`, { headers: api })).text())
    .toContain('% typed while trashed')
})

// Opening a trashed project lands in the banner, not in an error; once it is deleted for good, restoring says so.
test('opening a trashed project shows the banner, and a purged one says it was deleted', async ({ page, request }) => {
  const project = await (await request.post('/api/projects', { headers: api, data: { name: 'Gone' } })).json()
  expect((await request.delete(`/api/projects/${project.id}`, { headers: api })).ok()).toBeTruthy()

  await page.goto(`/p/${project.id}`)
  const banner = page.getByRole('status').filter({ hasText: 'This project is in the trash' })
  await expect(banner).toBeVisible()
  await expect(page.locator('[data-sonner-toast]')).toHaveCount(0)

  expect((await request.delete(`/api/projects/${project.id}/purge`, { headers: api })).ok()).toBeTruthy()
  await banner.getByRole('button', { name: 'Restore' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'This project was deleted.' })).toBeVisible()
})
