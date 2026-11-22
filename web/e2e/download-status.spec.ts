import { expect, test, type Page } from '@playwright/test'

const headers = { 'X-Refract': '1' }
const chip = (page: Page) => page.getByRole('button', { name: 'Compile status and problems', exact: true })

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('refract-prefs', JSON.stringify({ language: 'en' })))
})

const base = {
  ok: false,
  problems: [],
  output: '',
  timed_out: false,
  superseded: false,
  duration_ms: 3000,
  checkpoint: false,
}

async function open(page: Page, request: Parameters<Parameters<typeof test>[2]>[0]['request']) {
  const project = await (
    await request.post('/api/projects', { headers, data: { name: 'Downloads', template: 'article' } })
  ).json()
  await page.goto(`/p/${project.id}`)
  return project.id as string
}

// Mocks the compile and activity routes: the compile answers after `ms`, the
// activity follows the script as the compile goes on.
async function mock(page: Page, activities: object[], result: object, ms = 3500) {
  let n = 0
  await page.route('**/compile/activity', (route) => {
    const a = activities[Math.min(n++, activities.length - 1)]
    return route.fulfill({ json: a })
  })
  await page.route('**/api/projects/*/compile', async (route) => {
    if (route.request().method() !== 'POST') return route.fallback()
    await new Promise((r) => setTimeout(r, ms))
    return route.fulfill({ json: result })
  })
}

test('shows the download in progress and what was kept afterwards', async ({ page, request }) => {
  await mock(page, [{ state: 'downloading', file: 'siunitx.sty', downloaded: 3, retrying: false }], {
    ...base,
    ok: true,
    downloads: { fetched: 3, missing: [], offline: false },
  })
  await open(page, request)
  await page.getByRole('button', { name: 'Compile', exact: true }).click()
  await expect(chip(page)).toContainText('Downloading 3 files · siunitx.sty', { timeout: 5000 })
  await page.screenshot({ path: test.info().outputPath('downloading.png') })
  await expect(chip(page)).toContainText('Compiled', { timeout: 10_000 })
  await chip(page).click()
  await expect(page.getByText('Downloaded 3 files. They’re kept on this computer for offline use.')).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('downloaded.png') })
})

test('offline: explains what is missing and offers to try again', async ({ page, request }) => {
  await mock(page, [{ state: 'compiling', file: '', downloaded: 0, retrying: false }], {
    ...base,
    downloads: { fetched: 0, missing: ['siunitx.sty', 'tikz.sty'], offline: true },
  })
  await open(page, request)
  await page.getByRole('button', { name: 'Compile', exact: true }).click()
  await expect(chip(page)).toContainText('Needs a download', { timeout: 10_000 })
  await chip(page).click()
  await expect(page.getByText('This document needs files that aren’t on this computer yet')).toBeVisible()
  await expect(page.getByText('siunitx.sty, tikz.sty')).toBeVisible()
  await expect(page.getByText('Your document is saved. Connect to the internet and try again.')).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('needs-download.png') })
  const again = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/compile'))
  await page.getByRole('button', { name: 'Try again' }).click()
  await again
})

test('shows the retry while the package server is unreachable', async ({ page, request }) => {
  await mock(page, [{ state: 'downloading', file: 'a.sty', downloaded: 0, retrying: true }], {
    ...base,
    downloads: { fetched: 0, missing: ['a.sty'], offline: true },
  })
  await open(page, request)
  await page.getByRole('button', { name: 'Compile', exact: true }).click()
  await expect(chip(page)).toContainText('Can’t reach the package server…', { timeout: 5000 })
  await page.screenshot({ path: test.info().outputPath('retrying.png') })
})

test('a timeout while downloading says it was interrupted', async ({ page, request }) => {
  await mock(
    page,
    [{ state: 'compiling', file: '', downloaded: 0, retrying: false }],
    {
      ...base,
      timed_out: true,
      downloads: { fetched: 4, missing: [], offline: false, interrupted: true },
    },
    1500,
  )
  await open(page, request)
  await page.getByRole('button', { name: 'Compile', exact: true }).click()
  await expect(chip(page)).toContainText('Download interrupted', { timeout: 10_000 })
  await chip(page).click()
  await page.screenshot({ path: test.info().outputPath('interrupted.png') })
})
