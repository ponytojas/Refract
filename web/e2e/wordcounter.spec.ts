import { expect, test, type Page, type APIRequestContext } from '@playwright/test'

test.setTimeout(30_000)

const headers = { 'X-Refract': '1' }
const source = String.raw`\documentclass{article}
\begin{document}
\begin{abstract}
One two three four five six seven eight.
\end{abstract}
\section{Background}
Tail text.
\end{document}
`

async function openProject(
  page: Page,
  request: APIRequestContext,
  ai = false,
  initialSource = source,
  countEnabled = true,
) {
  await page.addInitScript(
    ({ countEnabled }) => {
      if (!localStorage.getItem('refract-prefs'))
        localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en', wordCount: countEnabled }))
    },
    { countEnabled },
  )
  await page.route('**/api/chatgpt/status', async (route) => {
    const response = await route.fetch()
    await route.fulfill({ json: { ...(await response.json()), ai_ready: ai } })
  })
  const project = await (await request.post('/api/projects', { headers, data: { name: 'Word counter' } })).json()
  await request.put(`/api/projects/${project.id}/file?path=main.tex`, { headers, data: initialSource })
  await request.patch(`/api/projects/${project.id}`, { headers, data: { main_file: 'main.tex' } })
  await page.goto(`/p/${project.id}`)
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  await expect(page.locator('.cm-content')).toBeVisible()
  return project
}

async function openLimit(page: Page, line = '\\begin{abstract}') {
  await page
    .locator('.cm-line')
    .filter({ hasText: line })
    .click({ button: 'right', position: { x: 10, y: 8 } })
  await page.getByRole('menuitem', { name: 'Word limit…', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveAccessibleName('Word limit')
}

async function abstractLimit(page: Page, max = '3') {
  await openLimit(page)
  await page.getByLabel('Maximum words', { exact: true }).fill(max)
  await page.getByRole('button', { name: 'Set limit', exact: true }).click()
  await expect(page.getByRole('dialog')).not.toBeVisible()
  await page
    .locator('.cm-line')
    .filter({ hasText: '\\begin{abstract}' })
    .click({ position: { x: 10, y: 8 } })
}

const shorter = (page: Page) => page.getByRole('button', { name: /^Suggest a shorter version:/ })

async function openWordCountSettings(page: Page) {
  await page.getByRole('button', { name: 'Account menu', exact: true }).click()
  await expect(page.getByRole('menuitemcheckbox', { name: 'Word count', exact: true })).toHaveCount(0)
  await page.getByRole('menuitem', { name: 'Settings', exact: true }).click()
  await page.getByRole('tab', { name: 'Editor', exact: true }).click()
}

test('editor settings toggle selection counts and remembers the preference after reload', async ({ page, request }) => {
  await openProject(page, request, false, source, false)
  await openWordCountSettings(page)
  const toggle = page.getByRole('switch', { name: 'Word count', exact: true })
  await expect(toggle).not.toBeChecked()
  await toggle.click()
  await expect(toggle).toBeChecked()
  await page.screenshot({ path: test.info().outputPath('editor-word-count-toggle.png'), animations: 'disabled' })
  await expect(page.getByText(/Counter: \d+ words?/)).toHaveCount(0)
  await page.keyboard.press('Escape')
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+a')
  await expect(page.getByText('Counter: 11 words', { exact: true })).toBeVisible()
  await openWordCountSettings(page)
  await toggle.click()
  await expect(toggle).not.toBeChecked()
  await expect(page.getByText(/Counter: \d+ words?/)).toHaveCount(0)
  await toggle.click()
  await expect(page.getByText('Counter: 11 words', { exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await page
    .locator('.cm-line')
    .filter({ hasText: 'One two three' })
    .dblclick({ position: { x: 10, y: 8 } })
  await expect(page.getByText('Counter: 1 word', { exact: true })).toBeVisible()
  await page.locator('.cm-content').click()
  await expect(page.getByText(/Counter: \d+ words?/)).toHaveCount(0)
  await page.reload()
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  await openWordCountSettings(page)
  await expect(toggle).toBeChecked()
  await expect(page.getByText(/Counter: \d+ words?/)).toHaveCount(0)
})

test('word count shortcut works before opening a file and in the editor, and syncs with settings', async ({
  page,
  request,
}) => {
  await openProject(page, request, false, source, false)
  await page.getByRole('button', { name: 'Close main.tex', exact: true }).click()
  await expect(page.getByText('Open a file from the sidebar', { exact: true })).toBeVisible()
  const hint = page.getByText('Toggle word count', { exact: true })
  await expect(hint).toBeVisible()
  await expect(hint.locator('..').getByRole('term')).toContainText(/⌘⇧M|Ctrl\+⇧M/)
  await page.keyboard.press('ControlOrMeta+Shift+m')
  await openWordCountSettings(page)
  const toggle = page.getByRole('switch', { name: 'Word count', exact: true })
  await expect(toggle).toBeChecked()
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: 'main.tex', exact: true }).first().click()
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+a')
  await expect(page.getByText('Counter: 11 words', { exact: true })).toBeVisible()
  const originalText = await page.locator('.cm-content').innerText()
  await page.keyboard.press('ControlOrMeta+Shift+m')
  await expect(page.getByText(/Counter: \d+ words?/)).toHaveCount(0)
  expect(await page.locator('.cm-content').innerText()).toBe(originalText)
  await openWordCountSettings(page)
  await expect(toggle).not.toBeChecked()
  await page.keyboard.press('ControlOrMeta+Shift+m')
  await expect(toggle).toBeChecked()
  await page.reload()
  await openWordCountSettings(page)
  await expect(toggle).toBeChecked()
})

test('include titles stays visible but disabled with the counter off and updates counts and limits persistently', async ({
  page,
  request,
}) => {
  await openProject(page, request, false, source, false)
  await openWordCountSettings(page)
  const counter = page.getByRole('switch', { name: 'Word count', exact: true })
  const titles = page.getByRole('switch', { name: 'Include titles', exact: true })
  await expect(titles).toBeVisible()
  await expect(titles).toBeDisabled()
  await counter.click()
  await expect(titles).toBeEnabled()
  await expect(titles).toBeChecked()
  await expect(page.getByText('Include titles', { exact: true })).toHaveCSS('font-weight', '400')
  await page.screenshot({ path: test.info().outputPath('include-titles.png'), animations: 'disabled' })
  await page.keyboard.press('Escape')
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+a')
  await expect(page.getByText('Counter: 11 words', { exact: true })).toBeVisible()
  await page
    .locator('.cm-line')
    .filter({ hasText: 'One two three' })
    .click({ button: 'right', position: { x: 10, y: 8 } })
  await page.getByRole('menuitem', { name: 'Word limit for selection…', exact: true }).click()
  await page.getByLabel('Maximum words', { exact: true }).fill('10')
  await page.getByRole('button', { name: 'Set limit', exact: true }).click()
  await expect(page.locator('.cm-word-excess')).toHaveCount(1)
  await openWordCountSettings(page)
  await titles.click()
  await expect(titles).not.toBeChecked()
  await page.keyboard.press('Escape')
  await expect(page.locator('.cm-word-excess')).toHaveCount(0)
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+a')
  await expect(page.getByText('Counter: 10 words', { exact: true })).toBeVisible()
  await page.keyboard.press('ControlOrMeta+Shift+m')
  await openWordCountSettings(page)
  await expect(titles).toBeVisible()
  await expect(titles).toBeDisabled()
  await counter.click()
  await expect(titles).not.toBeChecked()
  await page.reload()
  await page.getByRole('button', { name: 'main.tex', exact: true }).first().click()
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+a')
  await expect(page.getByText('Counter: 10 words', { exact: true })).toBeVisible()
  await expect(page.locator('.cm-word-excess')).toHaveCount(0)
  await openWordCountSettings(page)
  await expect(titles).not.toBeChecked()
  await titles.click()
  await page.keyboard.press('Escape')
  await expect(page.locator('.cm-word-excess')).toHaveCount(1)
  await expect(page.getByText('Counter: 11 words', { exact: true })).toBeVisible()
})

test('section context menu sets, edits and removes a limit with the counter on', async ({ page, request }) => {
  await openProject(page, request)
  await abstractLimit(page)
  await expect(page.locator('.cm-word-excess')).toHaveCount(5)
  await expect(shorter(page)).toBeVisible()
  await expect(page.getByText(/Counter: \d+ words?/)).toHaveCount(0)
  await expect(page.getByText('8 / 3', { exact: true })).toHaveCount(0)
  await page.screenshot({ path: test.info().outputPath('word-limit-warning.png') })
  await openLimit(page, 'One two three')
  await expect(page.getByLabel('Maximum words')).toHaveValue('3')
  await expect(page.getByRole('dialog').locator('input')).toHaveCount(1)
  await page.getByLabel('Maximum words').fill('7')
  await page.getByRole('button', { name: 'Set limit', exact: true }).click()
  await expect(page.locator('.cm-word-excess')).toHaveCount(1)
  await page.reload()
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  await expect(page.locator('.cm-word-excess')).toHaveCount(1)
  await openLimit(page)
  await expect(page.getByLabel('Maximum words')).toHaveValue('7')
  await page.screenshot({ path: test.info().outputPath('word-limit-dialog.png') })
  await page.getByRole('button', { name: 'Remove limit', exact: true }).click()
  await expect(page.locator('.cm-word-excess')).toHaveCount(0)
  await expect(shorter(page)).toHaveCount(0)
})

test('context menu also supports arbitrary selected fragments and limits follow typing', async ({ page, request }) => {
  await openProject(page, request)
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+a')
  await page
    .locator('.cm-line')
    .filter({ hasText: 'One two three' })
    .click({ button: 'right', position: { x: 10, y: 8 } })
  await page.getByRole('menuitem', { name: 'Word limit for selection…', exact: true }).click()
  await page.getByLabel('Maximum words', { exact: true }).fill('11')
  await page.getByRole('button', { name: 'Set limit', exact: true }).click()
  await expect(page.locator('.cm-word-excess')).toHaveCount(0)
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type('Extra prose')
  await expect(page.locator('.cm-word-excess')).toHaveCount(2)
  await expect(shorter(page)).toBeVisible()
})

test('floating action opens Assistant settings when AI is not connected', async ({ page, request }) => {
  await openProject(page, request)
  await abstractLimit(page)
  await shorter(page).click()
  await expect(page.getByRole('dialog')).toContainText('Assistant')
})

test('AI suggestions are counted and reviewed before replacement', async ({ page, request }) => {
  const project = await openProject(page, request, true)
  await page.route('**/api/quick', (route) => {
    const body = route.request().postDataJSON()
    expect(body.instruction).toContain('at most 3 readable words')
    expect(body.instruction).toContain('essential ideas')
    return route.fulfill({ json: { text: 'One essential idea.' } })
  })
  await abstractLimit(page)
  await shorter(page).click()
  await expect(page.getByText('8 → 3 words', { exact: true })).toBeVisible()
  await expect(page.locator('.cm-quick-ins').first()).toBeVisible()
  expect(await (await request.get(`/api/projects/${project.id}/file?path=main.tex`, { headers })).text()).toContain(
    'seven eight',
  )
  await page.getByRole('button', { name: 'Accept', exact: true }).click()
  await expect(page.locator('.cm-word-excess')).toHaveCount(0)
  await expect(shorter(page)).toHaveCount(0)
  await expect
    .poll(async () => (await request.get(`/api/projects/${project.id}/file?path=main.tex`, { headers })).text())
    .toContain('One essential idea.')
})

test('over-limit AI responses are rejected', async ({ page, request }) => {
  await openProject(page, request, true)
  await page.route('**/api/quick', (route) => route.fulfill({ json: { text: 'This response has too many words.' } }))
  await abstractLimit(page)
  await shorter(page).click()
  await expect(page.getByText('The suggestion does not meet the word limit. Try again.')).toBeVisible()
  await expect(page.locator('.cm-quick-ins')).toHaveCount(0)
  await expect(page.locator('.cm-word-excess')).toHaveCount(5)
})

test('AI results are discarded after an edit during the request', async ({ page, request }) => {
  await openProject(page, request, true)
  let release!: () => void
  const ready = new Promise<void>((resolve) => {
    release = resolve
  })
  let started!: () => void
  const requested = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/quick', async (route) => {
    started()
    await ready
    await route.fulfill({ json: { text: 'One essential idea.' } })
  })
  await abstractLimit(page)
  await shorter(page).click()
  await requested
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type('% new edit')
  release()
  await expect(page.getByText('The text changed while the assistant was working. Try again.')).toBeVisible()
  await expect(page.locator('.cm-quick-ins')).toHaveCount(0)
})

test('a reduction can be rejected without changing the document', async ({ page, request }) => {
  await openProject(page, request, true)
  await page.route('**/api/quick', (route) => route.fulfill({ json: { text: 'One essential idea.' } }))
  await abstractLimit(page)
  await shorter(page).click()
  await expect(page.getByText('8 → 3 words', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Reject', exact: true }).click()
  await expect(page.locator('.cm-word-excess')).toHaveCount(5)
  await expect(page.locator('.cm-quick-ins')).toHaveCount(0)
  await expect(shorter(page)).toBeVisible()
})

test('the same toggle controls the PDF selection count', async ({ page, request }) => {
  test.setTimeout(120_000)
  await openProject(page, request, false, source, false)
  await page.keyboard.press('ControlOrMeta+Enter')
  await expect(page.getByRole('button', { name: 'Compile status and problems' })).toContainText('Compiled', {
    timeout: 90_000,
  })
  const layer = page.locator('[data-page="1"] .refract-pdf-text')
  await expect(layer).toContainText('One two three')
  const selectPdfText = () =>
    layer.evaluate((el) => {
      const range = document.createRange()
      range.selectNodeContents(el)
      const selection = window.getSelection()!
      selection.removeAllRanges()
      selection.addRange(range)
      document.dispatchEvent(new Event('selectionchange'))
    })
  await selectPdfText()
  await expect(page.getByText(/Counter: \d+ words?/)).toHaveCount(0)
  await openWordCountSettings(page)
  const toggle = page.getByRole('switch', { name: 'Word count', exact: true })
  await toggle.click()
  await page.keyboard.press('Escape')
  await selectPdfText()
  await expect(page.getByText(/Counter: \d+ words/)).toBeVisible()
  await openWordCountSettings(page)
  await toggle.click()
  await page.keyboard.press('Escape')
  await selectPdfText()
  await expect(page.getByText(/Counter: \d+ words?/)).toHaveCount(0)
})

test('heading deletion and actual undo restore the limit', async ({ page, request }) => {
  await openProject(page, request)
  await abstractLimit(page)
  await page
    .locator('.cm-line')
    .filter({ hasText: '\\begin{abstract}' })
    .click({ position: { x: 10, y: 8 } })
  await page.keyboard.press('Home')
  await page.keyboard.press('Shift+End')
  await page.keyboard.press('Backspace')
  await expect(page.locator('.cm-word-excess')).toHaveCount(0)
  await page.keyboard.press('ControlOrMeta+z')
  await expect(page.locator('.cm-word-excess')).toHaveCount(5)
  await openLimit(page)
  await expect(page.getByLabel('Maximum words')).toHaveValue('3')
})

for (const folder of [false, true]) {
  test(`limits survive ${folder ? 'folder rename with a closed file' : 'file rename'} and reload`, async ({
    page,
    request,
  }) => {
    const project = await openProject(page, request)
    if (folder) {
      await request.put(`/api/projects/${project.id}/file?path=chapters/part.tex`, { headers, data: source })
      await page.reload()
      await page.getByRole('button', { name: 'chapters', exact: true }).click()
      await page.getByRole('button', { name: 'part.tex', exact: true }).first().click()
    }
    await abstractLimit(page)
    if (folder) await page.getByRole('button', { name: 'Close chapters/part.tex', exact: true }).click()
    await page
      .getByRole('button', { name: folder ? 'chapters' : 'main.tex', exact: true })
      .first()
      .click({ button: 'right' })
    await page.getByRole('menuitem', { name: 'Rename or move', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Rename or move' })
    await dialog.getByRole('textbox').fill(folder ? 'renamed' : 'renamed.tex')
    await dialog.getByRole('button', { name: 'Rename', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    await page.reload()
    if (folder) await page.getByRole('button', { name: 'renamed', exact: true }).click()
    await page
      .getByRole('button', { name: folder ? 'part.tex' : 'renamed.tex', exact: true })
      .first()
      .click()
    await expect(page.locator('.cm-word-excess')).toHaveCount(5)
    await openLimit(page)
    await expect(page.getByLabel('Maximum words')).toHaveValue('3')
  })
}

test('outside-budget cursors show no limit selector or shortening action', async ({ page, request }) => {
  await openProject(page, request)
  await abstractLimit(page)
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+Home')
  await expect(shorter(page)).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'Word limit assistant' })).toHaveCount(0)
  await page
    .locator('.cm-line')
    .filter({ hasText: '\\begin{abstract}' })
    .click({ position: { x: 10, y: 8 } })
  await expect(page.getByRole('combobox', { name: 'Choose a word limit' })).toHaveCount(0)
  await expect(shorter(page)).toBeVisible()
})

test('multiple budgets target the innermost section from headings and bodies', async ({ page, request }) => {
  const nested = String.raw`\documentclass{article}
\begin{document}
\section{Main}
Outer prose here.
\subsection{Child}
Child prose here.
\section{Other}
Other prose here.
\end{document}`
  await openProject(page, request, true, nested)
  for (const heading of ['\\section{Main}', '\\subsection{Child}', '\\section{Other}']) {
    await openLimit(page, heading)
    await page.getByLabel('Maximum words').fill('1')
    await page.getByRole('button', { name: 'Set limit', exact: true }).click()
  }
  for (const [line, name] of [
    ['\\section{Main}', 'Main'],
    ['Outer prose', 'Main'],
    ['\\subsection{Child}', 'Child'],
    ['Child prose', 'Child'],
    ['\\section{Other}', 'Other'],
  ]) {
    await page
      .locator('.cm-line')
      .filter({ hasText: line })
      .click({ position: { x: 10, y: 8 } })
    await expect(page.getByRole('button', { name: `Suggest a shorter version: ${name}`, exact: true })).toBeVisible()
  }
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+Home')
  await expect(shorter(page)).toHaveCount(0)
  await expect(page.getByRole('combobox')).toHaveCount(0)
  await page
    .locator('.cm-line')
    .filter({ hasText: 'Child prose' })
    .click({ position: { x: 10, y: 8 } })
  await page.route('**/api/quick', (route) => {
    expect(route.request().postDataJSON().selection.trim()).toBe('Child prose here.')
    return route.fulfill({ json: { text: 'Child.' } })
  })
  await shorter(page).click()
  await expect(page.getByText('3 → 1 words', { exact: true })).toBeVisible()
})

test('selecting a subsection up to the next heading keeps its own shortening target', async ({ page, request }) => {
  const nested = String.raw`\section{Main}
Outer prose here.
\subsection{Child}
Child prose here.
\subsection{Sibling}
Other prose here.`
  await openProject(page, request, true, nested)
  for (const heading of ['\\section{Main}', '\\subsection{Child}']) {
    await openLimit(page, heading)
    await page.getByLabel('Maximum words').fill('1')
    await page.getByRole('button', { name: 'Set limit', exact: true }).click()
  }
  await page
    .locator('.cm-line')
    .filter({ hasText: 'Child prose' })
    .click({ position: { x: 10, y: 8 } })
  await page.keyboard.press('Home')
  await page.keyboard.press('Shift+ArrowDown')
  await expect(page.getByRole('button', { name: 'Suggest a shorter version: Child', exact: true })).toBeVisible()
  await page.route('**/api/quick', (route) => {
    expect(route.request().postDataJSON().selection.trim()).toBe('Child prose here.')
    return route.fulfill({ json: { text: 'Child.' } })
  })
  await shorter(page).click()
  await expect(page.getByText('3 → 1 words', { exact: true })).toBeVisible()
})

test('subsection limit 104 protects its text from the parent limit of 11', async ({ page, request }) => {
  const nested = String.raw`\section{Introduccion}
Este texto contiene suficientes palabras para superar un limite pequeno y comprobar el resaltado de las palabras que sobran.
\subsection{Contexto}
Esta subseccion tiene su propio texto.`
  await openProject(page, request, false, nested)
  for (const [heading, max] of [
    ['\\section{Introduccion}', '11'],
    ['\\subsection{Contexto}', '104'],
  ]) {
    await openLimit(page, heading)
    await page.getByLabel('Maximum words').fill(max)
    await page.getByRole('button', { name: 'Set limit', exact: true }).click()
  }
  const intro = page.locator('.cm-line').filter({ hasText: 'Este texto' })
  const child = page.locator('.cm-line').filter({ hasText: 'Esta subseccion' })
  await expect(intro.locator('.cm-word-excess')).toHaveText([
    'comprobar',
    'el',
    'resaltado',
    'de',
    'las',
    'palabras',
    'que',
    'sobran',
  ])
  await expect(child.locator('.cm-word-excess')).toHaveCount(0)
  await page.reload()
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  await expect(child.locator('.cm-word-excess')).toHaveCount(0)
  await openLimit(page, '\\subsection{Contexto}')
  await page.getByLabel('Maximum words').fill('2')
  await page.getByRole('button', { name: 'Set limit', exact: true }).click()
  await expect(child.locator('.cm-word-excess')).toHaveText(['tiene', 'su', 'propio', 'texto'])
})

test('turning the counter off hides all limit features and keeps saved limits after reload', async ({
  page,
  request,
}) => {
  await openProject(page, request)
  await abstractLimit(page)
  await expect(page.locator('.cm-word-excess')).toHaveCount(5)
  await expect(shorter(page)).toBeVisible()
  await openWordCountSettings(page)
  await page.getByRole('switch', { name: 'Word count', exact: true }).click()
  await page.keyboard.press('Escape')
  await expect(page.locator('.cm-word-excess')).toHaveCount(0)
  await expect(shorter(page)).toHaveCount(0)
  await page
    .locator('.cm-line')
    .filter({ hasText: 'One two three' })
    .click({ button: 'right', position: { x: 10, y: 8 } })
  await expect(page.getByRole('menuitem', { name: /Word limit/ })).toHaveCount(0)
  await page.keyboard.press('Escape')
  await page.reload()
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  await expect(page.locator('.cm-word-excess')).toHaveCount(0)
  await expect(shorter(page)).toHaveCount(0)
  await openWordCountSettings(page)
  await page.getByRole('switch', { name: 'Word count', exact: true }).click()
  await page.keyboard.press('Escape')
  await expect(page.locator('.cm-word-excess')).toHaveCount(5)
  await openLimit(page)
  await expect(page.getByLabel('Maximum words')).toHaveValue('3')
})

test('turning the counter off discards its pending AI suggestion', async ({ page, request }) => {
  await openProject(page, request, true)
  await page.route('**/api/quick', (route) => route.fulfill({ json: { text: 'One essential idea.' } }))
  await abstractLimit(page)
  await shorter(page).click()
  await expect(page.getByText('8 → 3 words', { exact: true })).toBeVisible()
  await openWordCountSettings(page)
  await page.getByRole('switch', { name: 'Word count', exact: true }).click()
  await expect(page.locator('.cm-quick-ins')).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'Word limit assistant' })).toHaveCount(0)
  await page.getByRole('switch', { name: 'Word count', exact: true }).click()
  await page.keyboard.press('Escape')
  await expect(page.locator('.cm-word-excess')).toHaveCount(5)
  await expect(page.getByText('8 → 3 words', { exact: true })).toHaveCount(0)
})

test('renaming a heading updates the shortening label through undo, redo and reload', async ({ page, request }) => {
  const source = String.raw`\subsection{Con104texto}
Este texto supera su limite.`
  const project = await openProject(page, request, false, source)
  await openLimit(page, '\\subsection{Con104texto}')
  await page.getByLabel('Maximum words').fill('1')
  await page.getByRole('button', { name: 'Set limit', exact: true }).click()
  await page
    .locator('.cm-line')
    .filter({ hasText: '\\subsection{Con104texto}' })
    .click({ position: { x: 10, y: 8 } })
  await expect(shorter(page)).toHaveText('Suggest a shorter version: Con104texto')
  await page.keyboard.press('Home')
  for (let i = 0; i < 15; i++) await page.keyboard.press('ArrowRight')
  for (let i = 0; i < 3; i++) await page.keyboard.press('Delete')
  await expect(page.locator('.cm-content')).toContainText('\\subsection{Contexto}')
  await expect(shorter(page)).toHaveText('Suggest a shorter version: Contexto')
  await page.keyboard.press('ControlOrMeta+z')
  await expect(shorter(page)).toHaveText('Suggest a shorter version: Con104texto')
  await page.keyboard.press('ControlOrMeta+Shift+z')
  await expect(shorter(page)).toHaveText('Suggest a shorter version: Contexto')
  await expect
    .poll(async () => (await request.get(`/api/projects/${project.id}/file?path=main.tex`, { headers })).text())
    .toContain('\\subsection{Contexto}')
  await page.reload()
  await page.getByRole('button', { name: 'main.tex' }).first().click()
  await expect(shorter(page)).toHaveText('Suggest a shorter version: Contexto')
  await openLimit(page, '\\subsection{Contexto}')
  await expect(page.getByLabel('Maximum words')).toHaveValue('1')
})
