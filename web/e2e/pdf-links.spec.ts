import { expect, test, type Page } from '@playwright/test'

const headers = { 'X-Refract': '1' }

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en' })))
})

async function compile(page: Page) {
  await page.getByRole('button', { name: 'Compile', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Compile status and problems', exact: true })).toContainText(
    'Compiled',
    { timeout: 90_000 },
  )
  await expect
    .poll(() => page.locator('[data-page="1"] canvas').evaluate((c: HTMLCanvasElement) => c.width))
    .toBeGreaterThan(0)
}

test('article citations jump to the bibliography, including after zoom and recompilation', async ({
  page,
  request,
}) => {
  const project = await (
    await request.post('/api/projects', { headers, data: { name: 'Article links', template: 'article' } })
  ).json()
  await page.goto(`/p/${project.id}`)
  await compile(page)
  const citation = page.locator('[data-page="1"] a[data-pdf-destination="cite.example"]').first()
  await expect(citation).toBeVisible()
  await citation.click()
  const references = page.locator('[data-page="2"] .textLayer').getByText('References', { exact: true })
  await expect(references).toBeInViewport()
  await page.getByRole('button', { name: 'Zoom in', exact: true }).click()
  const pager = page.getByRole('textbox', { name: 'Page', exact: true })
  await pager.fill('1')
  await citation.focus()
  await citation.press('Enter')
  await expect(references).toBeInViewport()
  await compile(page)
  await pager.fill('1')
  await citation.click()
  await expect(references).toBeInViewport()
  const back = page.getByRole('button', { name: 'Return to citation on page 1', exact: true })
  await expect(back).toBeVisible()
  const pdfScroller = page.locator('[data-page="1"]').locator('..')
  expect((await back.boundingBox())!.x).toBeGreaterThanOrEqual((await pdfScroller.boundingBox())!.x)
  await page.screenshot({ path: test.info().outputPath('reference-return-link.png') })
  await back.click()
  await expect(citation).toBeInViewport()
  await expect(page.locator('[data-page="1"] .sync-highlight')).toBeInViewport()
})

test('PDF contents navigate to the heading within a page and preserve source-selection shortcuts', async ({
  page,
  request,
}) => {
  const project = await (await request.post('/api/projects', { headers, data: { name: 'PDF contents' } })).json()
  const source = String.raw`\documentclass{article}
\usepackage[colorlinks=true]{hyperref}
\begin{document}
\tableofcontents
\newpage
\section{First destination}
Top of the page.\par\vspace{10cm}
\section{Second destination}
A target further down the same page.\label{sec:second}
\newpage
\section{Last destination}
Another target on a different page. See Section~\ref{sec:second}.
\href{https://example.com/}{External website}
\end{document}`
  await request.put(`/api/projects/${project.id}/file?path=main.tex`, { headers, data: source })
  await request.patch(`/api/projects/${project.id}`, { headers, data: { main_file: 'main.tex' } })
  await page.goto(`/p/${project.id}`)
  await compile(page)
  const second = page.locator('[data-page="1"] a[data-pdf-destination="section.2"]').first()
  await expect(second).toBeVisible()
  await second.click()
  const heading = page.locator('[data-page="2"] .textLayer').getByText('Second destination', { exact: true })
  await expect(heading).toBeInViewport()
  const scroller = page.locator('[data-page="1"]').locator('..')
  const targetBounds = await heading.boundingBox()
  const viewerBounds = await scroller.boundingBox()
  expect(targetBounds!.y - viewerBounds!.y).toBeGreaterThanOrEqual(0)
  expect(targetBounds!.y - viewerBounds!.y).toBeLessThan(80)
  const before = await scroller.evaluate((el) => el.scrollTop)
  await page.waitForTimeout(250)
  expect(await scroller.evaluate((el) => el.scrollTop)).toBe(before)
  await page.getByRole('textbox', { name: 'Page', exact: true }).fill('1')
  await page.locator('[data-page="1"] a[data-pdf-destination="section.3"]').first().click()
  await expect(
    page.locator('[data-page="3"] .textLayer').getByText('Last destination', { exact: true }),
  ).toBeInViewport()
  const external = page.locator('[data-page="3"] a[href="https://example.com/"]')
  await expect(external).toHaveAttribute('target', '_blank')
  await expect(external).toHaveAttribute('rel', 'noopener noreferrer')
  // A plain text selection still works beside the link overlay.
  await page
    .locator('[data-page="3"] .textLayer span')
    .last()
    .evaluate((span) => {
      const range = document.createRange()
      range.selectNodeContents(span)
      const selection = window.getSelection()!
      selection.removeAllRanges()
      selection.addRange(range)
    })
  expect(await page.evaluate(() => window.getSelection()?.toString())).not.toBe('')
  // Modifier clicks on a link retain the existing inverse-SyncTeX shortcut.
  await page.route('**/synctex/inverse?*', (route) => route.fulfill({ json: { file: 'main.tex', line: 1 } }))
  await page.getByRole('textbox', { name: 'Page', exact: true }).fill('1')
  const inverse = page.waitForRequest((req) => req.url().includes('/synctex/inverse?'))
  await second.click({ modifiers: ['ControlOrMeta'] })
  expect(new URL((await inverse).url()).searchParams.get('page')).toBe('1')
  await expect(page.locator('.cm-content')).toBeVisible()
})

test('a wrapped figure-list entry highlights all its lines and every line navigates', async ({ page, request }) => {
  const project = await (
    await request.post('/api/projects', { headers, data: { name: 'Wrapped figure links' } })
  ).json()
  const source = String.raw`\documentclass{article}
\usepackage[textwidth=9cm]{geometry}
\usepackage[colorlinks=true]{hyperref}
\begin{document}
\listoffigures
\clearpage
\begin{figure}[ht]
\centering\rule{5cm}{3cm}
\caption{Schematic summary of the laminar distribution of molecularly defined interneurons in CA2 and dendritic profiles of PV+ and CCK+ interneurons. Adapted from an example research study, with a deliberately long caption spanning several lines.}
\end{figure}
\clearpage
\begin{figure}[ht]
\centering\rule{5cm}{3cm}
\caption{A separate figure entry that must not light up with the first one.}
\end{figure}
\end{document}`
  await request.put(`/api/projects/${project.id}/file?path=main.tex`, { headers, data: source })
  await request.patch(`/api/projects/${project.id}`, { headers, data: { main_file: 'main.tex' } })
  await page.goto(`/p/${project.id}`)
  await compile(page)
  const lines = page.locator('[data-page="1"] a[data-pdf-destination="figure.1"]')
  await expect.poll(() => lines.count()).toBeGreaterThan(2)
  await lines.nth(1).hover()
  await expect(lines.nth(1)).not.toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  const colours = () => lines.evaluateAll((links) => links.map((link) => getComputedStyle(link).backgroundColor))
  await expect
    .poll(colours)
    .toEqual(
      Array(await lines.count()).fill(await lines.nth(1).evaluate((link) => getComputedStyle(link).backgroundColor)),
    )
  const other = page.locator('[data-page="1"] a[data-pdf-destination="figure.2"]').first()
  await expect(other).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  await page.screenshot({ path: test.info().outputPath('wrapped-figure-link.png') })
  const count = await lines.count()
  for (let i = 0; i < count; i++) {
    await page.getByRole('textbox', { name: 'Page', exact: true }).fill('1')
    await lines.nth(i).click()
    await expect(
      page.locator('[data-page="2"] .textLayer').getByText('Figure 1:', { exact: false }).first(),
    ).toBeInViewport()
  }
})

test('references offer every citation, including two on the same page, and refresh after compilation', async ({
  page,
  request,
}) => {
  const project = await (await request.post('/api/projects', { headers, data: { name: 'Citation backlinks' } })).json()
  const source = String.raw`\documentclass{article}
\usepackage[colorlinks=true]{hyperref}
\begin{document}
First occurrence \cite{example}.\par\vspace{10cm}
Second occurrence \cite{example}.
\newpage
Third occurrence \cite{example}.
\newpage
\begin{thebibliography}{9}
\bibitem{example} A. Author. Title of the cited article. 2025.
\bibitem{unused} B. Author. Another article without citations. 2024.
\end{thebibliography}
\end{document}`
  await request.put(`/api/projects/${project.id}/file?path=main.tex`, { headers, data: source })
  await request.patch(`/api/projects/${project.id}`, { headers, data: { main_file: 'main.tex' } })
  await page.goto(`/p/${project.id}`)
  await compile(page)
  const pager = page.getByRole('textbox', { name: 'Page', exact: true })
  await pager.fill('3')
  const backlinks = page.getByRole('button', { name: 'Show 3 citations of this reference', exact: true })
  await expect(backlinks).toBeInViewport()
  await expect(page.locator('[data-pdf-backlink="cite.unused"]')).toHaveCount(0)
  await backlinks.click()
  await expect(page.getByRole('menuitem', { name: 'Citation 1 · page 1', exact: true })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Citation 2 · page 1', exact: true })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('reference-citation-menu.png') })
  await page.getByRole('menuitem', { name: 'Citation 3 · page 2', exact: true }).click()
  await expect(page.locator('[data-page="2"] a[data-pdf-destination="cite.example"]')).toBeInViewport()
  await page.waitForTimeout(250)
  await expect(page.locator('[data-page="2"] .sync-highlight')).toBeInViewport()
  await pager.fill('3')
  await backlinks.click()
  await page.getByRole('menuitem', { name: 'Citation 2 · page 1', exact: true }).click()
  const second = page.locator('[data-page="1"] a[data-pdf-destination="cite.example"]').nth(1)
  await expect(second).toBeInViewport()
  const scroller = page.locator('[data-page="1"]').locator('..')
  expect((await second.boundingBox())!.y - (await scroller.boundingBox())!.y).toBeLessThan(80)
  await request.put(`/api/projects/${project.id}/file?path=main.tex`, {
    headers,
    data: source.replace('Second occurrence \\cite{example}.', 'Second occurrence removed.'),
  })
  await compile(page)
  await pager.fill('3')
  const updated = page.getByRole('button', { name: 'Show 2 citations of this reference', exact: true })
  await expect(updated).toBeInViewport()
  await updated.focus()
  await updated.press('ArrowDown')
  await expect(page.getByRole('menuitem')).toHaveCount(2)
  await expect(page.getByRole('menuitem', { name: 'Citation 1 · page 1', exact: true })).toBeFocused()
  await page.keyboard.press('End')
  await expect(page.getByRole('menuitem', { name: 'Citation 2 · page 2', exact: true })).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page.locator('[data-page="2"] a[data-pdf-destination="cite.example"]')).toBeInViewport()
})
