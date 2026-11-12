import { expect, test, type Page, type APIRequestContext } from '@playwright/test'

const headers = { 'X-Refract': '1' }

async function project(page: Page, request: APIRequestContext, files: Record<string, string>) {
  await page.addInitScript(() =>
    localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en', wordCount: true })),
  )
  const p = await (
    await request.post('/api/projects', { headers, data: { name: 'Document counts and search' } })
  ).json()
  for (const [path, data] of Object.entries(files)) {
    expect(
      (await request.put(`/api/projects/${p.id}/file?path=${encodeURIComponent(path)}`, { headers, data })).ok(),
    ).toBeTruthy()
  }
  await request.patch(`/api/projects/${p.id}`, { headers, data: { main_file: 'main.tex' } })
  await page.goto(`/p/${p.id}`)
  return p
}

test('outline counts include nested files and update after edits and preference changes', async ({ page, request }) => {
  await project(page, request, {
    'main.tex': String.raw`\documentclass{article}
\begin{document}
\section{Introduction}
Alpha beta.\input{body}
\section{Conclusion}
Last word.
\end{document}`,
    'body.tex': String.raw`Before child.
\subsection{Details}
Gamma delta.`,
  })
  const outline = page.getByRole('navigation', { name: 'Outline', exact: true })
  await expect(outline.getByText('Document: 11 words', { exact: true })).toBeVisible()
  await expect(outline.getByRole('button', { name: 'Introduction', exact: true })).toContainText('8 words')
  await expect(outline.getByRole('button', { name: 'Details', exact: true })).toContainText('3 words')
  await expect(outline.getByRole('button', { name: 'Conclusion', exact: true })).toContainText('3 words')
  await outline.getByRole('button', { name: 'Details', exact: true }).click()
  await page.locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type(' Extra words.')
  await expect(outline.getByText('Document: 13 words', { exact: true })).toBeVisible()
  await expect(outline.getByRole('button', { name: 'Details', exact: true })).toContainText('5 words')
  await page.getByRole('button', { name: 'Account menu', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Settings', exact: true }).click()
  await page.getByRole('tab', { name: 'Editor', exact: true }).click()
  await page.getByRole('switch', { name: 'Include titles', exact: true }).click()
  await expect(outline.getByText('Document: 10 words', { exact: true })).toBeAttached()
  await page.keyboard.press('Escape')
  await page.keyboard.press('ControlOrMeta+Shift+m')
  await expect(outline.getByText(/Document:/)).toHaveCount(0)
  await expect(outline.getByRole('button', { name: 'Details', exact: true })).not.toContainText('words')
  await page.keyboard.press('ControlOrMeta+Shift+m')
  await expect(outline.getByText('Document: 10 words', { exact: true })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('document-counts.png'), animations: 'disabled' })
})

test('PDF search highlights and navigates all pages, handles zoom and refreshes after compilation', async ({
  page,
  request,
}) => {
  const source = String.raw`\documentclass{article}
\begin{document}
Needle first page.
\newpage
Needle second page.
\newpage
Needle final page.
\end{document}`
  const p = await project(page, request, { 'main.tex': source })
  await page.getByRole('button', { name: 'Compile', exact: true }).click()
  const viewer = page.getByRole('region', { name: 'PDF preview', exact: true })
  const show = viewer.getByRole('button', { name: 'Search PDF', exact: true })
  await expect(show).toBeEnabled({ timeout: 90_000 })
  await show.click()
  const search = viewer.getByRole('search', { name: 'Search PDF', exact: true })
  const input = search.getByRole('textbox', { name: 'Find in PDF', exact: true })
  await expect(input).toBeFocused()
  await input.fill('NEEDLE')
  await expect(search.getByRole('status')).toHaveText('1 of 3')
  await expect(viewer.locator('[data-page="1"] [data-pdf-search-hit][data-active="true"]').first()).toBeInViewport()
  await search.getByRole('button', { name: 'Next match', exact: true }).click()
  await expect(search.getByRole('status')).toHaveText('2 of 3')
  await expect(viewer.locator('[data-page="2"] [data-pdf-search-hit][data-active="true"]').first()).toBeInViewport()
  await input.focus()
  await input.press('Enter')
  await expect(search.getByRole('status')).toHaveText('3 of 3')
  await expect(viewer.locator('[data-page="3"] [data-pdf-search-hit][data-active="true"]').first()).toBeInViewport()
  await input.press('Shift+Enter')
  await expect(search.getByRole('status')).toHaveText('2 of 3')
  await input.press('ArrowDown')
  await expect(search.getByRole('status')).toHaveText('3 of 3')
  await expect(viewer.locator('[data-page="3"] [data-pdf-search-hit][data-active="true"]').first()).toBeInViewport()
  await input.press('ArrowDown')
  await expect(search.getByRole('status')).toHaveText('1 of 3')
  await input.press('ArrowUp')
  await expect(search.getByRole('status')).toHaveText('3 of 3')
  await input.press('ArrowUp')
  await expect(search.getByRole('status')).toHaveText('2 of 3')
  await expect(input).toBeFocused()
  await expect(input).toHaveValue('NEEDLE')
  await viewer.getByRole('button', { name: 'Fit', exact: true }).click()
  await page.getByRole('menuitem', { name: '100%', exact: true }).click()
  await expect(viewer.locator('[data-page="2"] [data-pdf-search-hit][data-active="true"]').first()).toBeInViewport()
  await input.fill('second page')
  await expect(search.getByRole('status')).toHaveText('1 of 1')
  await input.fill('not in this document')
  await expect(search.getByRole('status')).toHaveText('No matches')
  await expect(search.getByRole('button', { name: 'Next match', exact: true })).toBeDisabled()
  await expect(viewer.locator('[data-pdf-search-hit]')).toHaveCount(0)
  await input.fill('needle')
  await expect(search.getByRole('status')).toHaveText('1 of 3')
  await input.press('Shift+Enter')
  await expect(search.getByRole('status')).toHaveText('3 of 3')
  await request.put(`/api/projects/${p.id}/file?path=main.tex`, {
    headers,
    data: source.replace('Needle final', 'Other final'),
  })
  await page.getByRole('button', { name: 'Compile', exact: true }).click()
  await expect(search.getByRole('status')).toHaveText('1 of 2', { timeout: 90_000 })
  await expect(viewer.locator('[data-page="1"] [data-pdf-search-hit][data-active="true"]').first()).toBeInViewport()
  await page.screenshot({ path: test.info().outputPath('pdf-search.png'), animations: 'disabled' })
  await input.press('Escape')
  await expect(search).toHaveCount(0)
  await expect(viewer.locator('[data-pdf-search-hit]')).toHaveCount(0)
  await page.keyboard.press('ControlOrMeta+f')
  await expect(input).toBeFocused()
})
